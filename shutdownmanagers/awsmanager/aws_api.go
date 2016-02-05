package awsmanager

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/sqs"
)

type awsApi struct {
	autoScaling *autoscaling.AutoScaling
	ec2         *ec2.EC2
	sqs         *sqs.SQS

	config *AwsManagerConfig

	receiveMessageInput *sqs.ReceiveMessageInput
	queueUrl            string
}

func (api *awsApi) Init(config *AwsManagerConfig) error {
	awsSession := session.New(&aws.Config{
		Region:      &config.Region,
		Credentials: config.Credentials,
	})
	api.autoScaling = autoscaling.New(awsSession)
	api.ec2 = ec2.New(awsSession)
	api.sqs = sqs.New(awsSession)

	api.config = config

	if config.SqsQueueName != "" {
		queueUrl, err := api.getQueueURL(config.SqsQueueName)
		if err != nil {
			return err
		}
		api.queueUrl = queueUrl

		var maxNumberOfMessages int64 = 1
		var waitTimeSeconds int64 = 20
		api.receiveMessageInput = &sqs.ReceiveMessageInput{
			MaxNumberOfMessages: &maxNumberOfMessages,
			QueueUrl:            &api.queueUrl,
			WaitTimeSeconds:     &waitTimeSeconds,
		}
	}

	return nil
}

func (api *awsApi) getQueueURL(name string) (string, error) {
	queueURLOutput, err := api.sqs.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: &name,
	})
	if err != nil {
		return "", err
	}
	return *queueURLOutput.QueueUrl, nil
}

func (api *awsApi) ReceiveMessage() (*sqs.Message, error) {
	receiveMessageOutput, err := api.sqs.ReceiveMessage(api.receiveMessageInput)
	if err != nil {
		return nil, err
	}

	if len(receiveMessageOutput.Messages) < 1 {
		return nil, nil
	}

	return receiveMessageOutput.Messages[0], nil
}

func (api *awsApi) DeleteMessage(message *sqs.Message) error {
	_, err := api.sqs.DeleteMessage(&sqs.DeleteMessageInput{
		QueueUrl:      &api.queueUrl,
		ReceiptHandle: message.ReceiptHandle,
	})
	return err
}

func (api *awsApi) GetHost(instanceName string) (string, error) {
	describeInstancesInput := &ec2.DescribeInstancesInput{
		InstanceIds: []*string{&instanceName},
	}
	describeInstancesOutput, err := api.ec2.DescribeInstances(describeInstancesInput)
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
	if instance.PrivateIpAddress == nil {
		return "", fmt.Errorf("Instance private ip is nil.")
	}
	return *instance.PrivateIpAddress, nil
}

func (api *awsApi) GetMetadata(resId string) (string, error) {
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

func (api *awsApi) SendHeartbeat(autoscalingGroupName, lifecycleActionToken string) error {
	heartbeatInput := &autoscaling.RecordLifecycleActionHeartbeatInput{
		AutoScalingGroupName: &autoscalingGroupName,
		LifecycleActionToken: &lifecycleActionToken,
		LifecycleHookName:    &api.config.LifecycleHookName,
	}

	_, err := api.autoScaling.RecordLifecycleActionHeartbeat(heartbeatInput)
	return err
}

func (api *awsApi) CompleteLifecycleAction(autoscalingGroupName, lifecycleActionToken string) error {
	actionResult := "CONTINUE"

	actionInput := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  &autoscalingGroupName,
		LifecycleActionResult: &actionResult,
		LifecycleActionToken:  &lifecycleActionToken,
		LifecycleHookName:     &api.config.LifecycleHookName,
	}

	_, err := api.autoScaling.CompleteLifecycleAction(actionInput)
	return err
}
