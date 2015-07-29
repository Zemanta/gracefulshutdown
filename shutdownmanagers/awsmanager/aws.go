/*
AwsManager provides a listener for a message on SQS queue from the
Autoscaler requesting instance termination. It handles sending periodic
calls to RecordLifecycleActionHeartbeat and after all callbacks finish
a call to CompleteLifecycleAction.
*/
package awsmanager

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Zemanta/gracefulshutdown"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/sqs"
)

const (
	Name            = "AwsManager"
	defaultPingTime = time.Minute * 15
)

// AwsManager implements ShutdownManager interface that is added
// to GracefulShutdown. Initialize with NewAwsManager.
type AwsManager struct {
	ticker *time.Ticker
	gs     gracefulshutdown.GSInterface
	config *AwsManagerConfig
	api    awsApiInterface

	lifecycleActionToken string
	autoscalingGroupName string
}

type lifecycleHookMessage struct {
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	Service              string `json:"Service"`
	Time                 string `json:"Time"`
	AccountId            string `json:"AccountId"`
	LifecycleTransition  string `json:"LifecycleTransition"`
	RequestId            string `json:"RequestId"`
	LifecycleActionToken string `json:"LifecycleActionToken"`
	EC2InstanceId        string `json:"EC2InstanceId"`
	LifecycleHookName    string `json:"LifecycleHookName"`
}

type AwsManagerConfig struct {
	Credentials       *credentials.Credentials
	SqsQueueName      string
	LifecycleHookName string
	PingTime          time.Duration
	Port              uint16
	Region            string
	InstanceId        string
}

type awsApiInterface interface {
	Init(config *AwsManagerConfig) error
	GetMetadata(string) (string, error)
	ReceiveMessage() (*sqs.Message, error)
	DeleteMessage(*sqs.Message) error
	GetHost(string) (string, error)
	SendHeartbeat(string, string) error
	CompleteLifecycleAction(string, string) error
}

func (amc *AwsManagerConfig) clean() {
	if amc.PingTime == 0 {
		amc.PingTime = defaultPingTime
	}
}

// NewAwsManager initializes the AwsManager. credentials can be nil if
// credentials are set in ~/.aws/credntials, otherwise see aws-sdk-go
// documentation. queueName is name of the SQS queue where instance terminating
// message will be received. lifecycleHookName is name of lifecycleHook
// that we will listen for. pingTime is a period for sending
// RecordLifecycleActionHeartbeats.
func NewAwsManager(awsManagerConfig *AwsManagerConfig) *AwsManager {
	if awsManagerConfig == nil {
		awsManagerConfig = &AwsManagerConfig{}
	}
	awsManagerConfig.clean()
	return &AwsManager{
		config: awsManagerConfig,
		api:    &awsApi{},
	}
}

// GetName returns name of this ShutdownManager.
func (awsManager *AwsManager) GetName() string {
	return Name
}

// Start starts listening to sqs queue for termination messages. Will return
// error if aws metadata is not available or if invalid sqs queueName is given.
func (awsManager *AwsManager) Start(gs gracefulshutdown.GSInterface) error {
	awsManager.gs = gs

	if awsManager.config.Region == "" {
		availabilityZone, err := awsManager.api.GetMetadata("placement/availability-zone")
		if err != nil {
			return err
		}
		awsManager.config.Region = availabilityZone[:len(availabilityZone)-1]
	}

	if awsManager.config.InstanceId == "" {
		instanceId, err := awsManager.api.GetMetadata("instance-id")
		if err != nil {
			return err
		}
		awsManager.config.InstanceId = instanceId
	}

	if err := awsManager.api.Init(awsManager.config); err != nil {
		return err
	}

	if awsManager.config.SqsQueueName != "" {
		go awsManager.listenSQS()
	}

	if awsManager.config.Port != 0 {
		go awsManager.listenHTTP()
	}

	return nil
}

func (awsManager *AwsManager) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	bs, err := ioutil.ReadAll(req.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	message := string(bs)
	if awsManager.handleMessage(message) {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusBadRequest)
	}
}

func (awsManager *AwsManager) listenHTTP() {
	http.Handle("/", awsManager)
	http.ListenAndServe(fmt.Sprintf(":%d", awsManager.config.Port), nil)
}

func (awsManager *AwsManager) listenSQS() {
	for {
		message, err := awsManager.api.ReceiveMessage()
		awsManager.gs.ReportError(err)

		if message == nil {
			continue
		}

		if awsManager.handleMessage(*message.Body) {
			awsManager.api.DeleteMessage(message)
		}
	}
}

func (awsManager *AwsManager) handleMessage(message string) bool {
	hookMessage := &lifecycleHookMessage{}
	err := json.NewDecoder(strings.NewReader(message)).Decode(hookMessage)
	if err != nil {
		// not json message
		return false
	}

	if hookMessage.LifecycleHookName != awsManager.config.LifecycleHookName {
		// not our hook
		return false
	}

	if hookMessage.LifecycleTransition != "autoscaling:EC2_INSTANCE_TERMINATING" {
		// not terminating
		return false
	}

	if hookMessage.EC2InstanceId == awsManager.config.InstanceId {
		awsManager.lifecycleActionToken = hookMessage.LifecycleActionToken
		awsManager.autoscalingGroupName = hookMessage.AutoScalingGroupName

		go awsManager.gs.StartShutdown(awsManager)
		return true
	} else if awsManager.config.Port != 0 {
		awsManager.forwardMessage(hookMessage, message)
		return true
	}
	return false
}

func (awsManager *AwsManager) forwardMessage(hookMessage *lifecycleHookMessage, message string) {
	host, err := awsManager.api.GetHost(hookMessage.EC2InstanceId)
	if err != nil {
		awsManager.gs.ReportError(err)
		return
	}

	_, err = http.Post(fmt.Sprintf("http://%s:%d/", host, awsManager.config.Port), "application/json", strings.NewReader(message))
	awsManager.gs.ReportError(err)
}

// ShutdownStart calls Ping every pingTime
func (awsManager *AwsManager) ShutdownStart() error {
	awsManager.ticker = time.NewTicker(awsManager.config.PingTime)
	go func() {
		for {
			awsManager.gs.ReportError(awsManager.api.SendHeartbeat(
				awsManager.autoscalingGroupName,
				awsManager.lifecycleActionToken,
			))
			<-awsManager.ticker.C
		}
	}()
	return nil
}

// ShutdownFinish first stops the ticker for calling Ping,
// then calls aws api CompleteLifecycleAction.
func (awsManager *AwsManager) ShutdownFinish() error {
	awsManager.ticker.Stop()

	return awsManager.api.CompleteLifecycleAction(
		awsManager.autoscalingGroupName,
		awsManager.lifecycleActionToken,
	)
}
