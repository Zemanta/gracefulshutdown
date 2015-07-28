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

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
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

	lifecycleActionToken string
	autoscalingGroupName string

	autoScaling *autoscaling.AutoScaling
	ec2         *ec2.EC2
	sqs         *sqs.SQS
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
		availabilityZone, err := awsManager.getMetadata("placement/availability-zone")
		if err != nil {
			return err
		}
		awsManager.config.Region = availabilityZone[:len(availabilityZone)-1]
	}

	if awsManager.config.InstanceId == "" {
		instanceId, err := awsManager.getMetadata("instance-id")
		if err != nil {
			return err
		}
		awsManager.config.InstanceId = instanceId
	}

	awsConfig := &aws.Config{
		Region:      awsManager.config.Region,
		Credentials: awsManager.config.Credentials,
	}
	awsManager.autoScaling = autoscaling.New(awsConfig)
	awsManager.ec2 = ec2.New(awsConfig)
	awsManager.sqs = sqs.New(awsConfig)

	if awsManager.config.SqsQueueName != "" {
		if err := awsManager.listenSQS(); err != nil {
			return err
		}
	}

	if awsManager.config.Port != 0 {
		if err := awsManager.listenHTTP(); err != nil {
			return err
		}
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

func (awsManager *AwsManager) listenHTTP() error {
	http.Handle("/", awsManager)
	go http.ListenAndServe(fmt.Sprintf(":%d", awsManager.config.Port), nil)
	return nil
}

func (awsManager *AwsManager) listenSQS() error {
	queueURLOutput, err := awsManager.sqs.GetQueueURL(&sqs.GetQueueURLInput{
		QueueName: &awsManager.config.SqsQueueName,
	})
	if err != nil {
		return err
	}
	queueURL := queueURLOutput.QueueURL

	go func() {
		var maxNumberOfMessages int64 = 1
		var waitTimeSeconds int64 = 20
		receiveMessageInput := &sqs.ReceiveMessageInput{
			MaxNumberOfMessages: &maxNumberOfMessages,
			QueueURL:            queueURL,
			WaitTimeSeconds:     &waitTimeSeconds,
		}

		for {
			receiveMessageOutput, err := awsManager.sqs.ReceiveMessage(receiveMessageInput)
			awsManager.gs.ReportError(err)

			if len(receiveMessageOutput.Messages) < 1 {
				// no messages received
				continue
			}

			message := receiveMessageOutput.Messages[0]

			if awsManager.handleMessage(*message.Body) {
				_, err = awsManager.sqs.DeleteMessage(&sqs.DeleteMessageInput{
					QueueURL:      queueURL,
					ReceiptHandle: message.ReceiptHandle,
				})
				awsManager.gs.ReportError(err)
			}
		}
	}()

	return nil
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

func (awsManager *AwsManager) getTargetHost(instanceName string) (string, error) {
	describeInstancesInput := &ec2.DescribeInstancesInput{
		InstanceIDs: []*string{&instanceName},
	}
	describeInstancesOutput, err := awsManager.ec2.DescribeInstances(describeInstancesInput)
	if err != nil {
		return "", err
	}

	if len(describeInstancesOutput.Reservations) != 1 {
		return "", fmt.Errorf("Wrong number of reservations: %d", len(describeInstancesOutput.Reservations))
	}

	reservation := describeInstancesOutput.Reservations[0]
	if len(reservation.Instances) != 1 {
		return "", fmt.Errorf("Wrong number of instances: %d", len(reservation.Instances))
	}

	instance := reservation.Instances[0]
	if instance.PrivateIPAddress == nil {
		return "", fmt.Errorf("Instance private ip is nil.")
	}
	return *instance.PrivateIPAddress, nil
}

func (awsManager *AwsManager) forwardMessage(hookMessage *lifecycleHookMessage, message string) {
	host, err := awsManager.getTargetHost(hookMessage.EC2InstanceId)
	if err != nil {
		awsManager.gs.ReportError(err)
		return
	}

	_, err = http.Post(fmt.Sprintf("http://%s:%d/", host, awsManager.config.Port), "application/json", strings.NewReader(message))
	awsManager.gs.ReportError(err)
}

func (awsManager *AwsManager) getMetadata(resId string) (string, error) {
	client := http.Client{
		Timeout: time.Second * 5,
	}

	resp, err := client.Get("http://169.254.169.254/latest/meta-data/" + resId)
	if err != nil {
		return "", err
	}

	data, err := ioutil.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return "", err
	}

	return string(data), nil
}

// ShutdownStart calls Ping every pingTime
func (awsManager *AwsManager) ShutdownStart() error {
	awsManager.ticker = time.NewTicker(awsManager.config.PingTime)
	go func() {
		for {
			awsManager.gs.ReportError(awsManager.Ping())
			<-awsManager.ticker.C
		}
	}()
	return nil
}

// Ping calls aws api RecordLifecycleActionHeartbeat. It is called every
// pingTime once ShutdownStart is called.
func (awsManager *AwsManager) Ping() error {
	heartbeatInput := &autoscaling.RecordLifecycleActionHeartbeatInput{
		AutoScalingGroupName: &awsManager.autoscalingGroupName,
		LifecycleActionToken: &awsManager.lifecycleActionToken,
		LifecycleHookName:    &awsManager.config.LifecycleHookName,
	}

	_, err := awsManager.autoScaling.RecordLifecycleActionHeartbeat(heartbeatInput)
	return err
}

// ShutdownFinish first stops the ticker for calling Ping,
// then calls aws api CompleteLifecycleAction.
func (awsManager *AwsManager) ShutdownFinish() error {
	awsManager.ticker.Stop()

	actionResult := "CONTINUE"

	actionInput := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  &awsManager.autoscalingGroupName,
		LifecycleActionResult: &actionResult,
		LifecycleActionToken:  &awsManager.lifecycleActionToken,
		LifecycleHookName:     &awsManager.config.LifecycleHookName,
	}

	_, err := awsManager.autoScaling.CompleteLifecycleAction(actionInput)
	return err
}
