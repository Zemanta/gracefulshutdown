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

const Name = "AwsManager"

// AwsManager implements ShutdownManager interface that is added
// to GracefulShutdown. Initialize with NewAwsManager.
type AwsManager struct {
	ticker   *time.Ticker
	gs       gracefulshutdown.GSInterface
	pingTime time.Duration

	port uint16

	queueName         string
	lifecycleHookName string

	lifecycleActionToken string
	autoscalingGroupName string

	instanceId string
	region     string

	credentials *credentials.Credentials

	autoScaling *autoscaling.AutoScaling
	ec2         *ec2.EC2
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

// NewAwsManager initializes the AwsManager. credentials can be nil if
// credentials are set in ~/.aws/credntials, otherwise see aws-sdk-go
// documentation. queueName is name of the SQS queue where instance terminating
// message will be received. lifecycleHookName is name of lifecycleHook
// that we will listen for. pingTime is a period for sending
// RecordLifecycleActionHeartbeats.
func NewAwsManager(credentials *credentials.Credentials, queueName string, lifecycleHookName string, pingTime time.Duration) *AwsManager {
	return &AwsManager{
		queueName:         queueName,
		lifecycleHookName: lifecycleHookName,
		credentials:       credentials,
		pingTime:          pingTime,
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

	availabilityZone, err := awsManager.getMetadata("placement/availability-zone")
	if err != nil {
		return err
	}
	awsManager.region = availabilityZone[:len(availabilityZone)-1]

	awsManager.instanceId, err = awsManager.getMetadata("instance-id")
	if err != nil {
		return err
	}

	awsConfig := &aws.Config{
		Region:      awsManager.region,
		Credentials: awsManager.credentials,
	}
	awsManager.autoScaling = autoscaling.New(awsConfig)
	awsManager.ec2 = ec2.New(awsConfig)
	sqsInstance := sqs.New(awsConfig)

	queueURLOutput, err := sqsInstance.GetQueueURL(&sqs.GetQueueURLInput{QueueName: &awsManager.queueName})
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
			receiveMessageOutput, err := sqsInstance.ReceiveMessage(receiveMessageInput)
			gs.ReportError(err)

			if len(receiveMessageOutput.Messages) < 1 {
				// no messages received
				continue
			}

			message := receiveMessageOutput.Messages[0]
			awsManager.handleMessage(*message.Body)

			_, err = sqsInstance.DeleteMessage(&sqs.DeleteMessageInput{
				QueueURL:      queueURL,
				ReceiptHandle: message.ReceiptHandle,
			})
			gs.ReportError(err)
		}
	}()

	return nil
}

func (awsManager *AwsManager) handleMessage(message string) {
	hookMessage := &lifecycleHookMessage{}
	err := json.NewDecoder(strings.NewReader(message)).Decode(hookMessage)
	if err != nil {
		// not json message
		return
	}

	if awsManager.isMyShutdownMessage(hookMessage) {
		awsManager.gs.StartShutdown(awsManager)
	} else {
		awsManager.forwardMessage(hookMessage, message)
	}
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
	return *instance.PrivateIPAddress, nil
}

func (awsManager *AwsManager) forwardMessage(hookMessage *lifecycleHookMessage, message string) {
	host, err := awsManager.getTargetHost(hookMessage.EC2InstanceId)
	if err != nil {
		awsManager.gs.ReportError(err)
		return
	}

	_, err = http.Post(fmt.Sprintf("http://%s:%d/", host, awsManager.port), "application/json", strings.NewReader(message))
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

func (awsManager *AwsManager) isMyShutdownMessage(hookMessage *lifecycleHookMessage) bool {
	if hookMessage.LifecycleHookName != awsManager.lifecycleHookName {
		// not our hook
		return false
	}

	if hookMessage.EC2InstanceId != awsManager.instanceId {
		// not our instance
		return false
	}

	awsManager.lifecycleActionToken = hookMessage.LifecycleActionToken
	awsManager.autoscalingGroupName = hookMessage.AutoScalingGroupName

	return true
}

// ShutdownStart calls Ping every pingTime
func (awsManager *AwsManager) ShutdownStart() error {
	awsManager.ticker = time.NewTicker(awsManager.pingTime)
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
		LifecycleHookName:    &awsManager.lifecycleHookName,
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
		LifecycleHookName:     &awsManager.lifecycleHookName,
	}

	_, err := awsManager.autoScaling.CompleteLifecycleAction(actionInput)
	return err
}
