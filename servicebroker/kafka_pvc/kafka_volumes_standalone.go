package kafka_pvc

import (
	//"errors"
	"fmt"
	//marathon "github.com/gambol99/go-marathon"
	//kapi "golang.org/x/build/kubernetes/api"
	//"golang.org/x/build/kubernetes"
	//"golang.org/x/oauth2"
	//"net/http"
	//"net"
	"bytes"
	"encoding/json"
	"github.com/pivotal-cf/brokerapi"
	//"strconv"
	"strings"
	"time"
	//"crypto/sha1"
	//"encoding/base64"
	//"text/template"
	//"io"
	"io/ioutil"
	"os"
	"sync"

	"github.com/pivotal-golang/lager"

	//"k8s.io/kubernetes/pkg/util/yaml"
	kapi "k8s.io/kubernetes/pkg/api/v1"
	//routeapi "github.com/openshift/origin/route/api/v1"

	oshandler "github.com/asiainfoLDP/datafoundry_servicebroker_openshift/handler"
	dcapi "github.com/openshift/origin/deploy/api/v1"
)

//==============================================================
//
//==============================================================

const KafkaServcieBrokerName_Standalone = "Kafka_volumes_standalone"

func init() {
	oshandler.Register(KafkaServcieBrokerName_Standalone, &Kafka_freeHandler{})

	logger = lager.NewLogger(KafkaServcieBrokerName_Standalone)
	logger.RegisterSink(lager.NewWriterSink(os.Stdout, lager.DEBUG))
}

var logger lager.Logger

//==============================================================
//
//==============================================================

type Kafka_freeHandler struct{}

func (handler *Kafka_freeHandler) DoProvision(instanceID string, details brokerapi.ProvisionDetails, planInfo oshandler.PlanInfo, asyncAllowed bool) (brokerapi.ProvisionedServiceSpec, oshandler.ServiceInfo, error) {
	return newKafkaHandler().DoProvision(instanceID, details, planInfo, asyncAllowed)
}

func (handler *Kafka_freeHandler) DoLastOperation(myServiceInfo *oshandler.ServiceInfo) (brokerapi.LastOperation, error) {
	return newKafkaHandler().DoLastOperation(myServiceInfo)
}

func (handler *Kafka_freeHandler) DoDeprovision(myServiceInfo *oshandler.ServiceInfo, asyncAllowed bool) (brokerapi.IsAsync, error) {
	return newKafkaHandler().DoDeprovision(myServiceInfo, asyncAllowed)
}

func (handler *Kafka_freeHandler) DoBind(myServiceInfo *oshandler.ServiceInfo, bindingID string, details brokerapi.BindDetails) (brokerapi.Binding, oshandler.Credentials, error) {
	return newKafkaHandler().DoBind(myServiceInfo, bindingID, details)
}

func (handler *Kafka_freeHandler) DoUnbind(myServiceInfo *oshandler.ServiceInfo, mycredentials *oshandler.Credentials) error {
	return newKafkaHandler().DoUnbind(myServiceInfo, mycredentials)
}

//==============================================================
//
//==============================================================

type Kafka_Handler struct {
}

func newKafkaHandler() *Kafka_Handler {
	return &Kafka_Handler{}
}

func volumeBaseName_zk(instanceId string) string {
	return "kafka-zk-" + instanceId
}

func volumeBaseName_kafka(instanceId string) string {
	return "kafka-" + instanceId
}

func (handler *Kafka_Handler) DoProvision(instanceID string, details brokerapi.ProvisionDetails, planInfo oshandler.PlanInfo, asyncAllowed bool) (brokerapi.ProvisionedServiceSpec, oshandler.ServiceInfo, error) {
	//初始化到openshift的链接

	serviceSpec := brokerapi.ProvisionedServiceSpec{IsAsync: asyncAllowed}
	serviceInfo := oshandler.ServiceInfo{}

	//if asyncAllowed == false {
	//	return serviceSpec, serviceInfo, errors.New("Sync mode is not supported")
	//}
	serviceSpec.IsAsync = true

	//instanceIdInTempalte   := instanceID // todo: ok?
	instanceIdInTempalte := strings.ToLower(oshandler.NewThirteenLengthID())
	//serviceBrokerNamespace := ServiceBrokerNamespace
	serviceBrokerNamespace := oshandler.OC().Namespace()
	//kafkaUser := oshandler.NewElevenLengthID()
	//kafkaPassword := oshandler.GenGUID()
	zookeeperUser := "super" // oshandler.NewElevenLengthID()
	zookeeperPassword := oshandler.GenGUID()

	volumeBaseName_kafka := volumeBaseName_kafka(instanceIdInTempalte)
	volumeBaseName_zk := volumeBaseName_zk(instanceIdInTempalte)
	volumes := []oshandler.Volume{
		{
			Volume_size: planInfo.Volume_size,
			Volume_name: volumeBaseName_zk + "-1",
		},
		{
			Volume_size: planInfo.Volume_size,
			Volume_name: volumeBaseName_zk + "-2",
		},
		{
			Volume_size: planInfo.Volume_size,
			Volume_name: volumeBaseName_zk + "-3",
		},
		{
			Volume_size: planInfo.Volume_size,
			Volume_name: volumeBaseName_kafka + "-1",
		},
		{
			Volume_size: planInfo.Volume_size,
			Volume_name: volumeBaseName_kafka + "-2",
		},
	}

	println()
	println("instanceIdInTempalte = ", instanceIdInTempalte)
	println("serviceBrokerNamespace = ", serviceBrokerNamespace)
	println()

	serviceInfo.Url = instanceIdInTempalte
	serviceInfo.Database = serviceBrokerNamespace // may be not needed
	//serviceInfo.User = kafkaUser
	//serviceInfo.Password = kafkaPassword
	serviceInfo.Admin_user = zookeeperUser
	serviceInfo.Admin_password = zookeeperPassword

	serviceInfo.Volumes = volumes

	go func() {
		// create zk's volume
		result := oshandler.StartCreatePvcVolumnJob(
			volumeBaseName_zk,
			serviceInfo.Database,
			serviceInfo.Volumes[0:3],
		)

		err := <-result
		if err != nil {
			logger.Error("zookeeper create volume err:", err)
			return
		}

		println("create zookeeper resources ...")

		// todo: consider if DoDeprovision is called now, ...

		// create master zookeeper
		output, err := createZookeeperResources_Master(
			instanceIdInTempalte,
			serviceBrokerNamespace,
			zookeeperUser,
			zookeeperPassword,
			volumes,
		)
		if err != nil {
			fmt.Println("create Zookeeper resources error: ", err)
			logger.Error("create Zookeeper resources error: ", err)

			oshandler.DeleteVolumns(serviceInfo.Database, volumes[0:3])

			return
		}

		//start create kafka after zookeeper ok
		startKafkaOrchestrationJob(&kafkaOrchestrationJob{
			cancelled:  false,
			cancelChan: make(chan struct{}),

			serviceInfo:        &serviceInfo,
			zookeeperResources: output,
		})

	}()

	serviceSpec.DashboardURL = ""

	return serviceSpec, serviceInfo, nil
}

func (handler *Kafka_Handler) DoLastOperation(myServiceInfo *oshandler.ServiceInfo) (brokerapi.LastOperation, error) {
	// try to get state from running job
	volumeJob := oshandler.GetCreatePvcVolumnJob(volumeBaseName_kafka(myServiceInfo.Url))
	if volumeJob != nil {
		return brokerapi.LastOperation{
			State:       brokerapi.InProgress,
			Description: "in progress.",
		}, nil
	}

	ok := func(dc *dcapi.DeploymentConfig) bool {
		podCount, err := statRunningPodsByLabels(myServiceInfo.Database, dc.Labels)
		if err != nil {
			fmt.Println("statRunningPodsByLabels err:", err)
			return false
		}
		if dc == nil || dc.Name == "" || dc.Spec.Replicas == 0 || podCount < dc.Spec.Replicas {
			return false
		}
		n, _ := statRunningPodsByLabels(myServiceInfo.Database, dc.Labels)
		return n >= dc.Spec.Replicas
	}

	// assume in provisioning

	// the job may be finished or interrupted or running in another instance.

	master_res, _ := getKafkaResources_Master(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.Volumes) //, myServiceInfo.User, myServiceInfo.Password)

	if ok(&master_res.dc1) && ok(&master_res.dc2) {
		return brokerapi.LastOperation{
			State:       brokerapi.Succeeded,
			Description: "Succeeded!",
		}, nil
	} else {
		return brokerapi.LastOperation{
			State:       brokerapi.InProgress,
			Description: "In progress.",
		}, nil
	}

}

func (handler *Kafka_Handler) DoDeprovision(myServiceInfo *oshandler.ServiceInfo, asyncAllowed bool) (brokerapi.IsAsync, error) {
	go func() {
		job := getKafkaOrchestrationJob(myServiceInfo.Url)
		if job != nil {
			job.cancel()

			// wait job to exit
			for {
				time.Sleep(7 * time.Second)
				if nil == getKafkaOrchestrationJob(myServiceInfo.Url) {
					break
				}
			}
		}

		// ...

		println("to destroy zookeeper resources")

		zookeeper_res, _ := GetZookeeperResources_Master(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.Admin_user, myServiceInfo.Admin_password, nil)
		destroyZookeeperResources_Master(zookeeper_res, myServiceInfo.Database)

		// ...

		println("to destroy kafka resources")

		master_res, _ := getKafkaResources_Master(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.Volumes) //, myServiceInfo.User, myServiceInfo.Password)
		destroyKafkaResources_Master(master_res, myServiceInfo.Database)
	}()

	return brokerapi.IsAsync(false), nil
}

func (handler *Kafka_Handler) DoBind(myServiceInfo *oshandler.ServiceInfo, bindingID string, details brokerapi.BindDetails) (brokerapi.Binding, oshandler.Credentials, error) {
	// todo: handle errors

	//zookeeper_res, err := GetZookeeperResources_Master(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.Admin_user, myServiceInfo.Admin_password, nil)
	//if err != nil {
	//	return brokerapi.Binding{}, oshandler.Credentials{}, err
	//}
	//
	//zk_host, zk_port, err := zookeeper_res.ServiceHostPort(myServiceInfo.Database)
	//if err != nil {
	//	return brokerapi.Binding{}, oshandler.Credentials{}, nil
	//}
	//
	//master_res, err := getKafkaResources_Master(myServiceInfo.Url, myServiceInfo.Database, myServiceInfo.Volumes) //, myServiceInfo.User, myServiceInfo.Password)
	//if err != nil {
	//	return brokerapi.Binding{}, oshandler.Credentials{}, err
	//}
	//
	//kafka_port := oshandler.GetServicePortByName(&master_res.service, "kafka-port")
	//if kafka_port == nil {
	//	return brokerapi.Binding{}, oshandler.Credentials{}, errors.New("kafka-port port not found")
	//}
	//
	//host := fmt.Sprintf("%s.%s.svc.cluster.local", master_res.service.Name, myServiceInfo.Database)
	//port := strconv.Itoa(kafka_port.Port)
	////host := master_res.routeMQ.Spec.Host
	////port := "80"
	//
	mycredentials := oshandler.Credentials{
		//Uri: fmt.Sprintf("kafka: %s:%s zookeeper: %s:%s (SuperUser: %s, Password: %s)",
		//	host, port, zk_host, zk_port, myServiceInfo.Admin_user, myServiceInfo.Admin_password),
		//Hostname: host,
		//Port:     port,
		//Username: myServiceInfo.User,
		//Password: myServiceInfo.Password,
		// todo: need return zookeeper password?
	}

	myBinding := brokerapi.Binding{Credentials: mycredentials}
	//
	return myBinding, mycredentials, nil
}

func (handler *Kafka_Handler) DoUnbind(myServiceInfo *oshandler.ServiceInfo, mycredentials *oshandler.Credentials) error {
	// do nothing

	return nil
}

//===============================================================
//
//===============================================================

var kafkaOrchestrationJobs = map[string]*kafkaOrchestrationJob{}
var kafkaOrchestrationJobsMutex sync.Mutex

func getKafkaOrchestrationJob(instanceId string) *kafkaOrchestrationJob {
	kafkaOrchestrationJobsMutex.Lock()
	defer kafkaOrchestrationJobsMutex.Unlock()

	return kafkaOrchestrationJobs[instanceId]
}

func startKafkaOrchestrationJob(job *kafkaOrchestrationJob) {
	kafkaOrchestrationJobsMutex.Lock()
	defer kafkaOrchestrationJobsMutex.Unlock()

	if kafkaOrchestrationJobs[job.serviceInfo.Url] == nil {
		kafkaOrchestrationJobs[job.serviceInfo.Url] = job
		go func() {
			job.run()

			kafkaOrchestrationJobsMutex.Lock()
			delete(kafkaOrchestrationJobs, job.serviceInfo.Url)
			kafkaOrchestrationJobsMutex.Unlock()
		}()
	}
}

type kafkaOrchestrationJob struct {
	//instanceId string // use serviceInfo.

	cancelled   bool
	cancelChan  chan struct{}
	cancelMetex sync.Mutex

	serviceInfo *oshandler.ServiceInfo

	zookeeperResources *ZookeeperResources_Master
}

func (job *kafkaOrchestrationJob) cancel() {
	job.cancelMetex.Lock()
	defer job.cancelMetex.Unlock()

	if !job.cancelled {
		job.cancelled = true
		close(job.cancelChan)
	}
}

func (job *kafkaOrchestrationJob) run() {
	println("-- kafkaOrchestrationJob start --")

	result, cancel, err := watchZookeeperOrchestration(job.serviceInfo.Url, job.serviceInfo.Database, job.serviceInfo.Admin_user, job.serviceInfo.Admin_password, nil)
	if err != nil {
		//delete zookeeper resouces
		zookeeper_res, _ := GetZookeeperResources_Master(job.serviceInfo.Url, job.serviceInfo.Database, job.serviceInfo.Admin_user, job.serviceInfo.Admin_password, nil)
		destroyZookeeperResources_Master(zookeeper_res, job.serviceInfo.Database)
		//delete volumes of zookeeper
		oshandler.DeleteVolumns(job.serviceInfo.Database, job.serviceInfo.Volumes[0:3])
		return
	}

	var succeeded bool
	select {
	case <-job.cancelChan:
		close(cancel)
		return
	case succeeded = <-result:
		close(cancel)
		break
	}

	println("-- kafkaOrchestrationJob done, succeeded:", succeeded)

	if succeeded {
		volumeBaseName_kafka := volumeBaseName_kafka(job.serviceInfo.Url)

		println("to create kafka resources")

		result := oshandler.StartCreatePvcVolumnJob(
			volumeBaseName_kafka,
			job.serviceInfo.Database,
			job.serviceInfo.Volumes[3:5],
		)
		err := <-result
		if err != nil {
			logger.Error("kafka create volume err:", err)
			return
		}

		fmt.Println("------>to create kafka resources...")

		err = job.createKafkaResources_Master(job.serviceInfo.Url, job.serviceInfo.Database, job.serviceInfo.Volumes) //, job.serviceInfo.User, job.serviceInfo.Password)
		if err != nil {
			logger.Error("createKafkaResources_Master", err)
		} else {
			println("  succeeded to create kafka resources")
		}
	}
}

//=======================================================================
//
//=======================================================================

var KafkaTemplateData_Master []byte = nil

func loadKafkaResources_Master(instanceID, serviceBrokerNamespace /*, kafkaUser, kafkaPassword*/ string, res *kafkaResources_Master, volumes []oshandler.Volume) error {
	if KafkaTemplateData_Master == nil {
		f, err := os.Open("kafka-pvc.yaml")
		if err != nil {
			return err
		}
		KafkaTemplateData_Master, err = ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		kafka_image := oshandler.KafkaVolumeImage()
		kafka_image = strings.TrimSpace(kafka_image)
		if len(kafka_image) > 0 {
			KafkaTemplateData_Master = bytes.Replace(
				KafkaTemplateData_Master,
				[]byte("http://kafka-image-place-holder/kafka-openshift-orchestration"),
				[]byte(kafka_image),
				-1)
		}
	}

	peerPvcName0 := peerPvcName3(volumes)
	peerPvcName1 := peerPvcName4(volumes)

	// ...

	yamlTemplates := KafkaTemplateData_Master

	yamlTemplates = bytes.Replace(yamlTemplates, []byte("instanceid"), []byte(instanceID), -1)
	//yamlTemplates = bytes.Replace(yamlTemplates, []byte("local-service-postfix-place-holder"), []byte(serviceBrokerNamespace+".svc.cluster.local"), -1)

	yamlTemplates = bytes.Replace(yamlTemplates, []byte("kafka-pvc-name-replace1"), []byte(peerPvcName0), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("kafka-pvc-name-replace2"), []byte(peerPvcName1), -1)

	//println("========= Boot yamlTemplates ===========")
	//println(string(yamlTemplates))
	//println()

	decoder := oshandler.NewYamlDecoder(yamlTemplates)
	decoder.
		Decode(&res.dc1).
		Decode(&res.dc2).
		Decode(&res.svc1).
		Decode(&res.svc2).
		Decode(&res.svc3)

	return decoder.Err
}

type kafkaResources_Master struct {
	dc1 dcapi.DeploymentConfig
	dc2 dcapi.DeploymentConfig

	svc1 kapi.Service
	svc2 kapi.Service
	svc3 kapi.Service
}

func (job *kafkaOrchestrationJob) createKafkaResources_Master(instanceId, serviceBrokerNamespace /*, kafkaUser, kafkaPassword*/ string, volumes []oshandler.Volume) error {
	var input kafkaResources_Master
	err := loadKafkaResources_Master(instanceId, serviceBrokerNamespace /*, kafkaUser, kafkaPassword*/, &input, volumes)
	if err != nil {
		//return nil, err
		return err
	}

	var output kafkaResources_Master
	/*
		osr := oshandler.NewOpenshiftREST(oshandler.OC())

		// here, not use job.post
		prefix := "/namespaces/" + serviceBrokerNamespace
		osr.
			KPost(prefix + "/services", &input.service, &output.service).
			KPost(prefix + "/replicationcontrollers", &input.rc, &output.rc)

		if osr.Err != nil {
			logger.Error("createKafkaResources_Master", osr.Err)
		}

		return &output, osr.Err
	*/
	go func() {
		if err := job.opost(serviceBrokerNamespace, "deploymentconfigs", &input.dc1, &output.dc1); err != nil {
			logger.Error("createKafkaResources_Master.create dc1 err:", err)
			return
		}
		if err := job.opost(serviceBrokerNamespace, "deploymentconfigs", &input.dc2, &output.dc2); err != nil {
			logger.Error("createKafkaResources_Master.create dc1 err:", err)
			return
		}

		if err := job.kpost(serviceBrokerNamespace, "services", &input.svc1, &output.svc1); err != nil {
			logger.Error("createKafkaResources_Master.create service1 err:", err)
			return
		}
		if err := job.kpost(serviceBrokerNamespace, "services", &input.svc2, &output.svc2); err != nil {
			logger.Error("createKafkaResources_Master.create service2 err:", err)
			return
		}
		if err := job.kpost(serviceBrokerNamespace, "services", &input.svc3, &output.svc3); err != nil {
			logger.Error("createKafkaResources_Master.create service3 err:", err)
			return
		}
	}()

	return nil
}

func getKafkaResources_Master(instanceId, serviceBrokerNamespace /*, kafkaUser, kafkaPassword*/ string, volumes []oshandler.Volume) (*kafkaResources_Master, error) {
	var output kafkaResources_Master

	var input kafkaResources_Master
	err := loadKafkaResources_Master(instanceId, serviceBrokerNamespace /*, kafkaUser, kafkaPassword*/, &input, volumes)
	if err != nil {
		return &output, err
	}

	osr := oshandler.NewOpenshiftREST(oshandler.OC())

	prefix := "/namespaces/" + serviceBrokerNamespace
	osr.
		OGet(prefix+"/deploymentconfigs/"+input.dc1.Name, &output.dc1).
		OGet(prefix+"/deploymentconfigs/"+input.dc2.Name, &output.dc2).
		KGet(prefix+"/services/"+input.svc1.Name, &output.svc1).
		KGet(prefix+"/services/"+input.svc2.Name, &output.svc2).
		KGet(prefix+"/services/"+input.svc3.Name, &output.svc3)

	if osr.Err != nil {
		logger.Error("getKafkaResources_Master", osr.Err)
	}

	return &output, osr.Err
}

func destroyKafkaResources_Master(masterRes *kafkaResources_Master, serviceBrokerNamespace string) {
	// todo: add to retry queue on fail

	go func() { kdel(serviceBrokerNamespace, "deploymentconfigs", masterRes.dc1.Name) }()
	go func() { kdel(serviceBrokerNamespace, "deploymentconfigs", masterRes.dc1.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc1.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc2.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc3.Name) }()
}

//===============================================================
//
//===============================================================

func (job *kafkaOrchestrationJob) kpost(serviceBrokerNamespace, typeName string, body interface{}, into interface{}) error {
	println("to create ", typeName)

	uri := fmt.Sprintf("/namespaces/%s/%s", serviceBrokerNamespace, typeName)
	i, n := 0, 5
RETRY:
	if job.cancelled {
		return nil
	}

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KPost(uri, body, into)
	if osr.Err == nil {
		logger.Info("create " + typeName + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> create (%s) error", i, typeName), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("create (%s) failed", typeName), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func (job *kafkaOrchestrationJob) opost(serviceBrokerNamespace, typeName string, body interface{}, into interface{}) error {
	println("to create ", typeName)

	uri := fmt.Sprintf("/namespaces/%s/%s", serviceBrokerNamespace, typeName)
	i, n := 0, 5
RETRY:
	if job.cancelled {
		return nil
	}

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).OPost(uri, body, into)
	if osr.Err == nil {
		logger.Info("create " + typeName + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> create (%s) error", i, typeName), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("create (%s) failed", typeName), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func kdel(serviceBrokerNamespace, typeName, resName string) error {
	if resName == "" {
		return nil
	}

	println("to delete ", typeName, "/", resName)

	uri := fmt.Sprintf("/namespaces/%s/%s/%s", serviceBrokerNamespace, typeName, resName)
	i, n := 0, 5
RETRY:
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KDelete(uri, nil)
	if osr.Err == nil {
		logger.Info("delete " + uri + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> delete (%s) error", i, uri), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("delete (%s) failed", uri), osr.Err)
			return osr.Err
		}
	}

	return nil
}

func odel(serviceBrokerNamespace, typeName, resName string) error {
	if resName == "" {
		return nil
	}

	println("to delete ", typeName, "/", resName)

	uri := fmt.Sprintf("/namespaces/%s/%s/%s", serviceBrokerNamespace, typeName, resName)
	i, n := 0, 5
RETRY:
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).ODelete(uri, nil)
	if osr.Err == nil {
		logger.Info("delete " + uri + " succeeded")
	} else {
		i++
		if i < n {
			logger.Error(fmt.Sprintf("%d> delete (%s) error", i, uri), osr.Err)
			goto RETRY
		} else {
			logger.Error(fmt.Sprintf("delete (%s) failed", uri), osr.Err)
			return osr.Err
		}
	}

	return nil
}

/*
func kdel_rc (serviceBrokerNamespace string, rc *kapi.ReplicationController) {
	kdel (serviceBrokerNamespace, "replicationcontrollers", rc.Name)
}
*/

func kdel_rc(serviceBrokerNamespace string, rc *kapi.ReplicationController) {
	// looks pods will be auto deleted when rc is deleted.

	if rc == nil || rc.Name == "" {
		return
	}

	println("to delete pods on replicationcontroller", rc.Name)

	uri := "/namespaces/" + serviceBrokerNamespace + "/replicationcontrollers/" + rc.Name

	// modfiy rc replicas to 0

	zero := 0
	rc.Spec.Replicas = &zero
	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KPut(uri, rc, nil)
	if osr.Err != nil {
		logger.Error("modify HA rc", osr.Err)
		return
	}

	// start watching rc status

	statuses, cancel, err := oshandler.OC().KWatch(uri)
	if err != nil {
		logger.Error("start watching HA rc", err)
		return
	}

	go func() {
		for {
			status, _ := <-statuses

			if status.Err != nil {
				logger.Error("watch HA kafka rc error", status.Err)
				close(cancel)
				return
			} else {
				//logger.Debug("watch kafka HA rc, status.Info: " + string(status.Info))
			}

			var wrcs watchReplicationControllerStatus
			if err := json.Unmarshal(status.Info, &wrcs); err != nil {
				logger.Error("parse master HA rc status", err)
				close(cancel)
				return
			}

			if wrcs.Object.Status.Replicas <= 0 {
				break
			}
		}

		// ...

		kdel(serviceBrokerNamespace, "replicationcontrollers", rc.Name)
	}()

	return
}

type watchReplicationControllerStatus struct {
	// The type of watch update contained in the message
	Type string `json:"type"`
	// RC details
	Object kapi.ReplicationController `json:"object"`
}

func statRunningPodsByLabels(serviceBrokerNamespace string, labels map[string]string) (int, error) {

	println("to list pods in", serviceBrokerNamespace)

	uri := "/namespaces/" + serviceBrokerNamespace + "/pods"

	pods := kapi.PodList{}

	osr := oshandler.NewOpenshiftREST(oshandler.OC()).KList(uri, labels, &pods)
	if osr.Err != nil {
		return 0, osr.Err
	}

	nrunnings := 0

	for i := range pods.Items {
		pod := &pods.Items[i]

		println("\n pods.Items[", i, "].Status.Phase =", pod.Status.Phase, "\n")

		if pod.Status.Phase == kapi.PodRunning {
			nrunnings++
		}
	}

	return nrunnings, nil
}
