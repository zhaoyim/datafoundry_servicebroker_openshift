package storm

import (
	"errors"
	"fmt"
	//marathon "github.com/gambol99/go-marathon"
	//kapi "golang.org/x/build/kubernetes/api"
	//"golang.org/x/build/kubernetes"
	//"golang.org/x/oauth2"
	//"net/http"
	//"net"
	"bytes"
	"crypto/sha1"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
	//"text/template"
	//"io"
	"io/ioutil"
	"os"
	//"sync"

	//"k8s.io/kubernetes/pkg/util/yaml"
	routeapi "github.com/openshift/origin/route/api/v1"
	kapi "k8s.io/kubernetes/pkg/api/v1"

	oshandler "github.com/asiainfoLDP/datafoundry_servicebroker_openshift/handler"
)

//==============================================================
// interfaces for other service brokers which depend on zk
//==============================================================

func WatchZookeeperOrchestration(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword string) (result <-chan bool, cancel chan<- struct{}, err error) {
	var input ZookeeperResources_Master
	err = LoadZookeeperResources_Master(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword, &input)
	if err != nil {
		return
	}

	/*
		rc1 := &input.rc1
		rc2 := &input.rc2
		rc3 := &input.rc3
		uri1 := "/namespaces/" + serviceBrokerNamespace + "/replicationcontrollers/" + rc1.Name
		uri2 := "/namespaces/" + serviceBrokerNamespace + "/replicationcontrollers/" + rc2.Name
		uri3 := "/namespaces/" + serviceBrokerNamespace + "/replicationcontrollers/" + rc3.Name
		statuses1, cancel1, err := oshandler.OC().KWatch (uri1)
		if err != nil {
			return
		}
		statuses2, cancel2, err := oshandler.OC().KWatch (uri2)
		if err != nil {
			close(cancel1)
			return
		}
		statuses3, cancel3, err := oshandler.OC().KWatch (uri3)
		if err != nil {
			close(cancel1)
			close(cancel2)
			return
		}

		close_all := func() {
			close(cancel1)
			close(cancel2)
			close(cancel3)
		}
	*/

	var output ZookeeperResources_Master
	err = getZookeeperResources_Master(serviceBrokerNamespace, &input, &output)
	if err != nil {
		//close_all()
		return
	}

	rc1 := &output.rc1
	rc2 := &output.rc2
	rc3 := &output.rc3
	rc1.Status.Replicas = 0
	rc2.Status.Replicas = 0
	rc3.Status.Replicas = 0

	theresult := make(chan bool)
	result = theresult
	cancelled := make(chan struct{})
	cancel = cancelled

	go func() {
		ok := func(rc *kapi.ReplicationController) bool {
			if rc == nil || rc.Name == "" || rc.Spec.Replicas == nil {
				return false
			}

			if rc.Status.Replicas < *rc.Spec.Replicas {
				rc.Status.Replicas, _ = statRunningPodsByLabels(serviceBrokerNamespace, rc.Labels)

				println("rc = ", rc, ", rc.Status.Replicas = ", rc.Status.Replicas)
			}

			return rc.Status.Replicas >= *rc.Spec.Replicas
		}

		for {
			if ok(rc1) && ok(rc2) && ok(rc3) {
				theresult <- true

				//close_all()
				return
			}

			//var status oshandler.WatchStatus
			var valid bool
			//var rc **kapi.ReplicationController
			select {
			case <-cancelled:
				valid = false
			//case status, valid = <- statuses1:
			//	//rc = &rc1
			//	break
			//case status, valid = <- statuses2:
			//	//rc = &rc2
			//	break
			//case status, valid = <- statuses3:
			//	//rc = &rc3
			//	break
			case <-time.After(15 * time.Second):
				// bug: pod phase change will not trigger rc status change.
				// so need this case
				continue
			}

			/*
				if valid {
					if status.Err != nil {
						valid = false
						logger.Error("watch master rcs error", status.Err)
					} else {
						var wrcs watchReplicationControllerStatus
						if err := json.Unmarshal(status.Info, &wrcs); err != nil {
							valid = false
							logger.Error("parse master rc status", err)
						//} else {
						//	*rc = &wrcs.Object
						}
					}
				}

				println("> WatchZookeeperOrchestration valid:", valid)
			*/

			if !valid {
				theresult <- false

				//close_all()
				return
			}
		}
	}()

	return
}

//=======================================================================
// the zookeeper functions may be called by outer packages
//=======================================================================

var ZookeeperTemplateData_Master []byte = nil

func LoadZookeeperResources_Master(instanceID, serviceBrokerNamespace, zookeeperUser, zookeeperPassword string, res *ZookeeperResources_Master) error {
	/*
		if ZookeeperTemplateData_Master == nil {
			f, err := os.Open("zookeeper.yaml")
			if err != nil {
				return err
			}
			ZookeeperTemplateData_Master, err = ioutil.ReadAll(f)
			if err != nil {
				return err
			}
			zookeeper_image := oshandler.ZookeeperImage()
			zookeeper_image = strings.TrimSpace(zookeeper_image)
			if len(zookeeper_image) > 0 {
				ZookeeperTemplateData_Master = bytes.Replace(
					ZookeeperTemplateData_Master,
					[]byte("http://zookeeper-image-place-holder/zookeeper-openshift-orchestration"),
					[]byte(zookeeper_image),
					-1)
			}
		}
	*/

	if ZookeeperTemplateData_Master == nil {
		f, err := os.Open("zookeeper-with-dashboard.yaml")
		if err != nil {
			return err
		}
		ZookeeperTemplateData_Master, err = ioutil.ReadAll(f)
		if err != nil {
			return err
		}
		endpoint_postfix := oshandler.EndPointSuffix()
		endpoint_postfix = strings.TrimSpace(endpoint_postfix)
		if len(endpoint_postfix) > 0 {
			ZookeeperTemplateData_Master = bytes.Replace(
				ZookeeperTemplateData_Master,
				[]byte("endpoint-postfix-place-holder"),
				[]byte(endpoint_postfix),
				-1)
		}
		//zookeeper_image := oshandler.ZookeeperExhibitorImage()
		zookeeper_image := oshandler.ZookeeperImage()
		zookeeper_image = strings.TrimSpace(zookeeper_image)
		if len(zookeeper_image) > 0 {
			ZookeeperTemplateData_Master = bytes.Replace(
				ZookeeperTemplateData_Master,
				[]byte("http://zookeeper-exhibitor-image-place-holder/zookeeper-exhibitor-openshift-orchestration"),
				[]byte(zookeeper_image),
				-1)
		}
	}

	// ...

	// invalid operation sha1.Sum(([]byte)(zookeeperPassword))[:] (slice of unaddressable value)
	//sum := (sha1.Sum([]byte(zookeeperPassword)))[:]
	//zoo_password := zookeeperUser + ":" + base64.StdEncoding.EncodeToString (sum)

	sum := sha1.Sum([]byte(fmt.Sprintf("%s:%s", zookeeperUser, zookeeperPassword)))
	zoo_password := fmt.Sprintf("%s:%s", zookeeperUser, base64.StdEncoding.EncodeToString(sum[:]))

	yamlTemplates := ZookeeperTemplateData_Master

	yamlTemplates = bytes.Replace(yamlTemplates, []byte("instanceid"), []byte(instanceID), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("super:password-place-holder"), []byte(zoo_password), -1)
	yamlTemplates = bytes.Replace(yamlTemplates, []byte("local-service-postfix-place-holder"),
		[]byte(serviceBrokerNamespace+oshandler.ServiceDomainSuffix(true)), -1)

	//println("========= Boot yamlTemplates ===========")
	//println(string(yamlTemplates))
	//println()

	decoder := oshandler.NewYamlDecoder(yamlTemplates)
	decoder.
		Decode(&res.service).
		Decode(&res.svc1).
		Decode(&res.svc2).
		Decode(&res.svc3).
		Decode(&res.rc1).
		Decode(&res.rc2).
		Decode(&res.rc3).
		Decode(&res.route)

	return decoder.Err
}

type ZookeeperResources_Master struct {
	service kapi.Service

	svc1 kapi.Service
	svc2 kapi.Service
	svc3 kapi.Service
	rc1  kapi.ReplicationController
	rc2  kapi.ReplicationController
	rc3  kapi.ReplicationController

	route routeapi.Route
}

func (masterRes *ZookeeperResources_Master) ServiceHostPort(serviceBrokerNamespace string) (string, string, error) {

	client_port := oshandler.GetServicePortByName(&masterRes.service, "client")
	if client_port == nil {
		return "", "", errors.New("client port not found")
	}

	host := fmt.Sprintf("%s.%s.%s", masterRes.service.Name, serviceBrokerNamespace, oshandler.ServiceDomainSuffix(false))
	port := strconv.Itoa(client_port.Port)

	return host, port, nil
}

func CreateZookeeperResources_Master(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword string) (*ZookeeperResources_Master, error) {
	var input ZookeeperResources_Master
	err := LoadZookeeperResources_Master(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword, &input)
	if err != nil {
		return nil, err
	}

	var output ZookeeperResources_Master

	osr := oshandler.NewOpenshiftREST(oshandler.OC())

	// here, not use job.post
	prefix := "/namespaces/" + serviceBrokerNamespace
	osr.
		KPost(prefix+"/services", &input.service, &output.service).
		KPost(prefix+"/services", &input.svc1, &output.svc1).
		KPost(prefix+"/services", &input.svc2, &output.svc2).
		KPost(prefix+"/services", &input.svc3, &output.svc3).
		KPost(prefix+"/replicationcontrollers", &input.rc1, &output.rc1).
		KPost(prefix+"/replicationcontrollers", &input.rc2, &output.rc2).
		KPost(prefix+"/replicationcontrollers", &input.rc3, &output.rc3).
		OPost(prefix+"/routes", &input.route, &output.route)

	if osr.Err != nil {
		logger.Error("createZookeeperResources_Master", osr.Err)
	}

	return &output, osr.Err
}

func GetZookeeperResources_Master(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword string) (*ZookeeperResources_Master, error) {
	var output ZookeeperResources_Master

	var input ZookeeperResources_Master
	err := LoadZookeeperResources_Master(instanceId, serviceBrokerNamespace, zookeeperUser, zookeeperPassword, &input)
	if err != nil {
		return &output, err
	}

	err = getZookeeperResources_Master(serviceBrokerNamespace, &input, &output)
	return &output, err
}

func getZookeeperResources_Master(serviceBrokerNamespace string, input, output *ZookeeperResources_Master) error {
	osr := oshandler.NewOpenshiftREST(oshandler.OC())

	prefix := "/namespaces/" + serviceBrokerNamespace
	osr.
		KGet(prefix+"/services/"+input.service.Name, &output.service).
		KGet(prefix+"/services/"+input.svc1.Name, &output.svc1).
		KGet(prefix+"/services/"+input.svc2.Name, &output.svc2).
		KGet(prefix+"/services/"+input.svc3.Name, &output.svc3).
		KGet(prefix+"/replicationcontrollers/"+input.rc1.Name, &output.rc1).
		KGet(prefix+"/replicationcontrollers/"+input.rc2.Name, &output.rc2).
		KGet(prefix+"/replicationcontrollers/"+input.rc3.Name, &output.rc3).
		// old bsi has no route, so get route may be error
		OGet(prefix+"/routes/"+input.route.Name, &output.route)

	if osr.Err != nil {
		logger.Error("getZookeeperResources_Master", osr.Err)
	}

	return osr.Err
}

func DestroyZookeeperResources_Master(masterRes *ZookeeperResources_Master, serviceBrokerNamespace string) {
	// todo: add to retry queue on fail

	go func() { kdel(serviceBrokerNamespace, "services", masterRes.service.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc1.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc2.Name) }()
	go func() { kdel(serviceBrokerNamespace, "services", masterRes.svc3.Name) }()
	go func() { kdel_rc(serviceBrokerNamespace, &masterRes.rc1) }()
	go func() { kdel_rc(serviceBrokerNamespace, &masterRes.rc2) }()
	go func() { kdel_rc(serviceBrokerNamespace, &masterRes.rc3) }()
	go func() { odel(serviceBrokerNamespace, "routes", masterRes.route.Name) }()
}

// https://hub.docker.com/r/mbabineau/zookeeper-exhibitor/
// https://hub.docker.com/r/netflixoss/exhibitor/

// todo:
// set ACL: https://godoc.org/github.com/samuel/go-zookeeper/zk#Conn.SetACL
// github.com/samuel/go-zookeeper/zk

/*
bin/zkCli.sh 127.0.0.1:2181
bin/zkCli.sh -server sb-instanceid-zk:2181

echo conf|nc localhost 2181
echo cons|nc localhost 2181
echo ruok|nc localhost 2181
echo srst|nc localhost 2181
echo crst|nc localhost 2181
echo dump|nc localhost 2181
echo srvr|nc localhost 2181
echo stat|nc localhost 2181
echo mntr|nc localhost 2181
*/

/* need this?

# zoo.cfg

# Enable regular purging of old data and transaction logs every 24 hours
autopurge.purgeInterval=24
autopurge.snapRetainCount=5

The last two autopurge.* settings are very important for production systems.
They instruct ZooKeeper to regularly remove (old) data and transaction logs.
The default ZooKeeper configuration does not do this on its own,
and if you do not set up regular purging ZooKeeper will quickly run out of disk space.

*/
