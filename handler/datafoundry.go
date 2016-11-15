package handler

import (
	"os"
	"encoding/json"
	"time"
	"io/ioutil"
	"errors"
	//"fmt"
	kapi "k8s.io/kubernetes/pkg/api/v1"
	"fmt"
	"sync"
)

var dfProxyApiPrefix string
func DfProxyApiPrefix() string {
	if dfProxyApiPrefix == "" {
		addr := os.Getenv("DATAFOUNDRYPROXYADDR")
		if addr == "" {
			logger.Error("int dfProxyApiPrefix error:", errors.New("DATAFOUNDRYPROXYADDR env is not set"))
		}

		dfProxyApiPrefix = "http://" + addr + "/lapi/v1"
	}
	return dfProxyApiPrefix
}

const DfRequestTimeout = time.Duration(8) * time.Second

func dfRequest(method, url, bearerToken string, bodyParams interface{}, into interface{}) (err error) {
	var body []byte
	if bodyParams != nil {
		body, err = json.Marshal(bodyParams)
		if err != nil {
			return
		}
	}
	
	res, err := request(DfRequestTimeout, method, url, bearerToken, body)
	if err != nil {
		return
	}
	defer res.Body.Close()
	
	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return
	}
	
	//println("22222 len(data) = ", len(data), " , res.StatusCode = ", res.StatusCode)

	if res.StatusCode < 200 || res.StatusCode >= 400 {
		err = errors.New(string(data))
	} else {
		if into != nil {
			//println("into data = ", string(data), "\n")
		
			err = json.Unmarshal(data, into)
		}
	}
	
	return
}

type VolumnCreateOptions struct {
	Name string     `json:"name,omitempty"`
	Size int        `json:"size,omitempty"`
	kapi.ObjectMeta `json:"metadata,omitempty"`
}

func CreateVolumn(volumnName string, size int) error {
	oc := OC()

	url := DfProxyApiPrefix() + "/namespaces/" + oc.Namespace() + "/volumes"

	options := &VolumnCreateOptions{
		volumnName,
		size,
		kapi.ObjectMeta {
			Annotations: map[string]string {
				"dadafoundry.io/create-by": oc.username,
			},
		},
	}
	
	err := dfRequest("POST", url, oc.BearerToken(), options, nil)

	return err
}

func DeleteVolumn(volumnName string) error {
	oc := OC()

	url := DfProxyApiPrefix() + "/namespaces/" + oc.Namespace() + "/volumes/" + volumnName
	
	err := dfRequest("DELETE", url, oc.BearerToken(), nil, nil)
	
	return err
}

//====================

type watchPvcStatus struct {
	// The type of watch update contained in the message
	Type string `json:"type"`
	// Pod details
	Object kapi.PersistentVolumeClaim `json:"object"`
}

func WaitUntilPvcIsBound(namespace, pvcName string, stopWatching <-chan struct{}) error {
	select {
	case <- stopWatching:
		return errors.New("cancelled by calleer")
	default:
	}
	
	uri := "/namespaces/" + namespace + "/persistentvolumeclaims/" + pvcName
	statuses, cancel, err := OC().KWatch (uri)
	if err != nil {
		return err
	}
	defer close(cancel)
	
	getPvcChan := make(chan *kapi.PersistentVolumeClaim, 1)
	go func() {
		// the pvc may be already bound initially.
		// so simulate this get request result as a new watch event.
		
		select {
		case <- stopWatching:
			return
		case <- time.After(3 * time.Second):
			pvc := &kapi.PersistentVolumeClaim{}
			osr := NewOpenshiftREST(OC()).KGet(uri, pvc)
//fmt.Println("WaitUntilPvcIsBound, get pvc, osr.Err=", osr.Err)
			if osr.Err == nil {
				getPvcChan <- pvc
			} else {
				getPvcChan <- nil
			}
		}
	}()

	for {
		var pvc *kapi.PersistentVolumeClaim
		select {
		case <- stopWatching:
			return errors.New("cancelled by calleer")
		case pvc = <- getPvcChan:
		case status, _ := <- statuses:
			if status.Err != nil {
				return status.Err
			}
			
			var wps watchPvcStatus
			if err := json.Unmarshal(status.Info, &wps); err != nil {
				return err
			}
			
			pvc = &wps.Object
		}

		if pvc == nil {
			// get return 404 from above goroutine
			return errors.New("pvc not found")
		}

		// assert pvc != nil

//fmt.Println("WaitUntilPvcIsBound, pvc.Phase=", pvc.Status.Phase, ", pvc=", *pvc)
		
		if pvc.Status.Phase != kapi.ClaimPending {
			//println("watch pvc phase: ", pvc.Status.Phase)
			
			if pvc.Status.Phase != kapi.ClaimBound {
				return errors.New("pvc phase is neither pending nor bound: " + string(pvc.Status.Phase))
			}
			
			break
		}
	}
	
	return nil
}

//=======================================================================
// 
//=======================================================================

// todo: it is best to save jobs in mysql firstly, ...
// now, when the server instance is terminated, jobs are lost.

var pvcVolumnCreatingJobs = map[string]*CreatePvcVolumnJob{}
var pvcVolumnCreatingJobsMutex sync.Mutex

func GetCreatePvcVolumnJob (instanceId string) *CreatePvcVolumnJob {
	pvcVolumnCreatingJobsMutex.Lock()
	job := pvcVolumnCreatingJobs[instanceId]
	pvcVolumnCreatingJobsMutex.Unlock()
	
	return job
}

func StartCreatePvcVolumnJob (
		volumeName string, 
		volumeSize int, 
		serviceInfo *ServiceInfo,
		) <-chan error {
	
	job := &CreatePvcVolumnJob {
		volumeName: volumeName,
		volumeSize: volumeSize,
		serviceInfo: serviceInfo,
	}

	c := make(chan error)
	
	pvcVolumnCreatingJobsMutex.Lock()
	defer pvcVolumnCreatingJobsMutex.Unlock()
	
	if pvcVolumnCreatingJobs[volumeName] == nil {
		pvcVolumnCreatingJobs[volumeName] = job
		go func() {
			job.run(c)
			
			pvcVolumnCreatingJobsMutex.Lock()
			delete(pvcVolumnCreatingJobs, volumeName)
			pvcVolumnCreatingJobsMutex.Unlock()
		}()
	}

	return c
}

type CreatePvcVolumnJob struct {
	cancelled bool
	cancelChan chan struct{}
	cancelMetex sync.Mutex
	
	volumeName  string
	volumeSize  int
	serviceInfo *ServiceInfo

}

func (job *CreatePvcVolumnJob) Cancel() {
	job.cancelMetex.Lock()
	defer job.cancelMetex.Unlock()
	
	if ! job.cancelled {
		job.cancelled = true
		close (job.cancelChan)
	}
}

func (job *CreatePvcVolumnJob) run(c chan<- error) {
	println("startCreatePvcVolumnJob ...")

	println("CreateVolumn", job.volumeName, "...")

	err := CreateVolumn(job.volumeName, job.volumeSize)
	if err != nil {
		println("CreateVolumn", job.volumeName, "esrror: ", err)
		c <- fmt.Errorf("CreateVolumn error: ", err)
		return
	}

	println("WaitUntilPvcIsBound", job.volumeName, "...")

	// watch pvc until bound

	err = WaitUntilPvcIsBound(job.serviceInfo.Database, job.volumeName, job.cancelChan)
	if err != nil {
		println("WaitUntilPvcIsBound", job.volumeName, "error: ", err)

		println("DeleteVolumn", job.volumeName, "...")

		// todo: on error
		DeleteVolumn(job.volumeName)

		c <- fmt.Errorf("WaitUntilPvcIsBound", job.volumeName, "error: ", err)

		return
	}

	c <- nil
}


