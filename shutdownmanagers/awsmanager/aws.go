/*
AwsManager provides a listener for a message on SQS queue from the
Autoscaler requesting instance termination. If http port is specified,
it can forward received message to correct instance via http.
It handles sending periodic calls to RecordLifecycleActionHeartbeat
and after all callbacks finish a call to CompleteLifecycleAction.
*/
package awsmanager

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Zemanta/gracefulshutdown"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/sqs"
)

const (
	Name = "AwsManager"

	defaultPingTime       = time.Minute * 15
	defaultBackOff        = 500.0
	defaultForwardRetries = 10
	defaultServeRetries   = 3
)

// AwsManager implements ShutdownManager interface that is added
// to GracefulShutdown. Initialize with NewAwsManager.
type AwsManager struct {
	ticker   *time.Ticker
	gs       gracefulshutdown.GSInterface
	config   *AwsManagerConfig
	api      awsApiInterface
	listener net.Listener

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

// AwsManagerConfig provides configuration options for AwsManager.
type AwsManagerConfig struct {
	// Credentials can be nil if credentials are set in ~/.aws/credntials,
	// otherwise see aws-sdk-go documentation.
	Credentials *credentials.Credentials

	// Name of sqs queue to listen for terminating message. Can be empty
	// to disable listening to sqs.
	SqsQueueName string

	// LifecycleHookName is name of the lifecycleHook that will be listened for.
	LifecycleHookName string

	// PingTime is period for sending RecordLifecycleActionHeartbeats.
	// Default is 15 minutes.
	PingTime time.Duration

	// Port on which to listen for terminating messages over http.
	// If 0, http is disabled.
	Port uint16

	// BackOff is time for backup when retrying http listener and http forward
	BackOff float64

	// NumServeRetries is number of retries for http listener
	// -1 for no retries, 0 is default value
	NumServeRetries int

	// NumForwardRetries is number of retries for http forward
	// -1 for no retries, 0 is default value
	NumForwardRetries int

	// Region and InstanceId are optional. If empty they get collected from
	// EC2 instance metadata.
	Region     string
	InstanceId string
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
	if amc.BackOff == 0 {
		amc.BackOff = defaultBackOff
	}
	if amc.NumServeRetries == 0 {
		amc.NumServeRetries = defaultServeRetries
	} else if amc.NumServeRetries < 0 {
		amc.NumServeRetries = 0
	}
	if amc.NumForwardRetries == 0 {
		amc.NumForwardRetries = defaultForwardRetries
	} else if amc.NumForwardRetries < 0 {
		amc.NumForwardRetries = 0
	}
}

// NewAwsManager initializes the AwsManager. See AwsManagerConfig for
// configuration options.
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

// Start starts listening to sqs queue and http for termination messages.
// Will return error if aws metadata is not available
// or if invalid sqs queueName is given.
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

	if awsManager.config.Port != 0 {
		if err := awsManager.listenHTTP(); err != nil {
			return err
		}
	}

	awsManager.gs.AddShutdownCallback(awsManager)

	if awsManager.config.SqsQueueName != "" {
		go awsManager.listenSQS()
	}

	return nil
}

// OnShutdown closes http server on shutdown
func (awsManager *AwsManager) OnShutdown(shutdownManager string) error {
	awsManager.listener.Close()
	return nil
}

// ServeHTTP is used for receiving messages over http.
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

func (awsManager *AwsManager) listenHTTP() error {
	var err error

	for i := 0; i < awsManager.config.NumServeRetries+1; i++ {
		awsManager.listener, err = net.Listen("tcp", fmt.Sprintf(":%d", awsManager.config.Port))
		if err == nil {
			break
		}

		time.Sleep(awsManager.backOffDuration(i))
	}
	if err != nil {
		return err
	}

	go http.Serve(awsManager.listener, awsManager)

	return nil
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
		if err := awsManager.forwardMessage(hookMessage, message); err != nil {
			awsManager.gs.ReportError(err)
		}
		return true
	}
	return false
}

func (awsManager *AwsManager) forwardMessage(hookMessage *lifecycleHookMessage, message string) error {
	host, err := awsManager.api.GetHost(hookMessage.EC2InstanceId)
	if err != nil {
		return err
	}

	for i := 0; i < awsManager.config.NumForwardRetries+1; i++ {
		_, err = http.Post(fmt.Sprintf("http://%s:%d/", host, awsManager.config.Port), "application/json", strings.NewReader(message))
		if err == nil {
			break
		}

		time.Sleep(awsManager.backOffDuration(i))
	}
	return err
}

func (awsManager *AwsManager) backOffDuration(i int) time.Duration {
	rand := rand.Float64() + 0.5
	try := float64(i + 1)
	return time.Duration(awsManager.config.BackOff*try*rand) * time.Millisecond
}

// ShutdownStart starts sending LifecycleActionHeartbeat every PingTime.
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

// ShutdownFinish first stops the ticker for sending heartbeats,
// then calls aws api CompleteLifecycleAction.
func (awsManager *AwsManager) ShutdownFinish() error {
	awsManager.ticker.Stop()

	return awsManager.api.CompleteLifecycleAction(
		awsManager.autoscalingGroupName,
		awsManager.lifecycleActionToken,
	)
}
