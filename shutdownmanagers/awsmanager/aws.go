package awsmanager

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Zemanta/gracefulshutdown"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/sqs"
)

type AwsManager struct {
	queueName         string
	lifecycleHookName string

	lifecycleActionToken string
	autoscalingGroupName string

	instanceId string
	region     string

	credentials *credentials.Credentials

	autoScaling *autoscaling.AutoScaling
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

func NewAwsManager(credentials *credentials.Credentials, queueName string, lifecycleHookName string) *AwsManager {
	return &AwsManager{
		queueName:         queueName,
		lifecycleHookName: lifecycleHookName,
		credentials:       credentials,
	}
}

func (awsManager *AwsManager) Start(ssi gracefulshutdown.StartShutdownInterface) error {
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
			if err != nil {
				// TODO
			}

			if len(receiveMessageOutput.Messages) < 1 {
				// no messages received
				continue
			}

			message := receiveMessageOutput.Messages[0]

			if awsManager.isMyShutdownMessage(*message.Body) {
				_, err = sqsInstance.DeleteMessage(&sqs.DeleteMessageInput{
					QueueURL:      queueURL,
					ReceiptHandle: message.ReceiptHandle,
				})
				if err != nil {
					// TODO
				}

				ssi.StartShutdown(awsManager)
				return
			}
		}
	}()

	return nil
}

func (awsManager *AwsManager) getMetadata(resId string) (string, error) {
	resp, err := http.Get("http://169.254.169.254/latest/meta-data/" + resId)
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

func (awsManager *AwsManager) isMyShutdownMessage(message string) bool {
	hookMessage := &lifecycleHookMessage{}
	err := json.NewDecoder(strings.NewReader(message)).Decode(hookMessage)
	if err != nil {
		// not json message
		return false
	}

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

func (awsManager *AwsManager) Ping() {
	heartbeatInput := &autoscaling.RecordLifecycleActionHeartbeatInput{
		AutoScalingGroupName: &awsManager.autoscalingGroupName,
		LifecycleActionToken: &awsManager.lifecycleActionToken,
		LifecycleHookName:    &awsManager.lifecycleHookName,
	}

	_, err := awsManager.autoScaling.RecordLifecycleActionHeartbeat(heartbeatInput)
	if err != nil {
		// TODO
		fmt.Println("Heartbeat:", err)
	}
}

func (awsManager *AwsManager) ShutdownFinish() {
	actionResult := "CONTINUE"

	actionInput := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  &awsManager.autoscalingGroupName,
		LifecycleActionResult: &actionResult,
		LifecycleActionToken:  &awsManager.lifecycleActionToken,
		LifecycleHookName:     &awsManager.lifecycleHookName,
	}

	_, err := awsManager.autoScaling.CompleteLifecycleAction(actionInput)
	if err != nil {
		// TODO
		fmt.Println("Complete:", err)
	}
}
