package awsmanager

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Zemanta/gracefulshutdown"

	"github.com/aws/aws-sdk-go/service/sqs"
)

type GSFunc func(sm gracefulshutdown.ShutdownManager)

func (f GSFunc) StartShutdown(sm gracefulshutdown.ShutdownManager) {
	f(sm)
}

func (f GSFunc) ReportError(err error) {

}

func (f GSFunc) AddShutdownCallback(shutdownCallback gracefulshutdown.ShutdownCallback) {

}

type awsApiMock struct {
	heartbeatChannel chan int
	completeChannel  chan int
	initChannel      chan int
	deleteChannel    chan int
	messageSent      bool
}

func newAwsApiMock() *awsApiMock {
	return &awsApiMock{
		heartbeatChannel: make(chan int, 100),
		completeChannel:  make(chan int, 100),
		initChannel:      make(chan int, 100),
		deleteChannel:    make(chan int, 100),
		messageSent:      false,
	}
}

func (api *awsApiMock) Init(config *AwsManagerConfig) error {
	api.initChannel <- 1
	return nil
}

func (api *awsApiMock) ReceiveMessage() (*sqs.Message, error) {
	if !api.messageSent {
		msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"my-lifecycle-hook"}`
		message := &sqs.Message{
			Body: &msg,
		}
		api.messageSent = true
		return message, nil
	}
	time.Sleep(time.Hour)
	return nil, nil
}

func (api *awsApiMock) DeleteMessage(message *sqs.Message) error {
	api.deleteChannel <- 1
	return nil
}

func (api *awsApiMock) GetHost(instanceName string) (string, error) {
	return "127.0.0.1", nil
}

func (api *awsApiMock) GetMetadata(resId string) (string, error) {
	return "metadata", nil
}

func (api *awsApiMock) SendHeartbeat(autoscalingGroupName, lifecycleActionToken string) error {
	api.heartbeatChannel <- 1
	return nil
}

func (api *awsApiMock) CompleteLifecycleAction(autoscalingGroupName, lifecycleActionToken string) error {
	api.completeChannel <- 1
	return nil
}

func TestNewAwsManager(t *testing.T) {
	awsManager := NewAwsManager(nil)

	if awsManager.config == nil {
		t.Error("Config not initialized.")
	}

	if awsManager.config.PingTime != defaultPingTime {
		t.Error("Defualt ping time not set.")
	}
}

func TestStart(t *testing.T) {
	awsManager := NewAwsManager(&AwsManagerConfig{})
	mock := newAwsApiMock()
	awsManager.api = mock
	gs := GSFunc(func(sm gracefulshutdown.ShutdownManager) {
	})

	if err := awsManager.Start(gs); err != nil {
		t.Error("Error in start:", err)
	}

	if len(mock.initChannel) != 1 {
		t.Error("Api init not called.")
	}

	if awsManager.config.Region != "metadat" {
		t.Error("Region not initialized from metadata.")
	}

	if awsManager.config.InstanceId != "metadata" {
		t.Error("Instance id not initialized from metadata.")
	}
}

func TestListenSqs(t *testing.T) {
	c := make(chan int, 100)

	awsManager := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	awsManager.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {
		c <- 1
	})
	mock := newAwsApiMock()
	awsManager.api = mock

	go awsManager.listenSQS()
	time.Sleep(time.Millisecond * 5)

	if len(mock.deleteChannel) != 1 {
		t.Error("Received message not deleted.")
	}

	if len(c) != 1 {
		t.Error("Shutdown not started.")
	}
}

func TestHttpServe(t *testing.T) {
	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"my-lifecycle-hook"}`

	req, _ := http.NewRequest("POST", "", strings.NewReader(msg))
	w := httptest.NewRecorder()

	c := make(chan int, 100)
	awsManager := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	awsManager.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {
		c <- 1
	})
	mock := newAwsApiMock()
	awsManager.api = mock

	awsManager.ServeHTTP(w, req)

	time.Sleep(time.Millisecond * 5)

	if len(c) != 1 {
		t.Error("Shutdown not started.")
	}

	if w.Code != 200 {
		t.Error("Http did not return success.")
	}
}

func TestHttpServeInvalid(t *testing.T) {
	msg := `message`

	req, _ := http.NewRequest("POST", "", strings.NewReader(msg))
	w := httptest.NewRecorder()

	c := make(chan int, 100)
	awsManager := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	awsManager.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {
		c <- 1
	})
	mock := newAwsApiMock()
	awsManager.api = mock

	awsManager.ServeHTTP(w, req)

	time.Sleep(time.Millisecond * 5)

	if len(c) != 0 {
		t.Error("Shutdown started.")
	}

	if w.Code != 400 {
		t.Error("Http did not return failure.")
	}
}

func TestHttpForward(t *testing.T) {
	c := make(chan int, 100)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c <- 1
	}))
	defer ts.Close()

	port, _ := strconv.Atoi(strings.Split(ts.URL, ":")[2])

	awsManager := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
		Port:              uint16(port),
	})
	awsManager.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})
	mock := newAwsApiMock()
	awsManager.api = mock

	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-752c5f8a","LifecycleHookName":"my-lifecycle-hook"}`

	if !awsManager.handleMessage(msg) {
		t.Error("Should forward message.")
	}

	if len(c) != 1 {
		t.Error("Should get message on http server.")
	}
}

func TestStartFinishShutdown(t *testing.T) {
	awsManager := NewAwsManager(&AwsManagerConfig{
		PingTime: time.Millisecond * 2,
	})
	awsManager.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})
	mock := newAwsApiMock()
	awsManager.api = mock

	awsManager.ShutdownStart()
	time.Sleep(time.Millisecond * 3)
	awsManager.ShutdownFinish()

	if len(mock.completeChannel) != 1 {
		t.Error("Shutdown finish did not call CompleteLifecycleAction.")
	}

	if len(mock.heartbeatChannel) != 2 {
		t.Error("SendHeartbeat did not get called twice.")
	}
}

func TestCorrectShutdownMessage(t *testing.T) {
	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"my-lifecycle-hook"}`

	c := make(chan int, 100)

	aws := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	aws.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {
		c <- 1
	})

	if !aws.handleMessage(msg) {
		t.Error("Should be correct shutdown message.")
	}

	time.Sleep(time.Millisecond * 5)

	if aws.lifecycleActionToken != "my-lifecycle-token" {
		t.Error("gs.lifecycleActionToken should be my-lifecycle-token, but is ", aws.lifecycleActionToken)
	}

	if aws.autoscalingGroupName != "my-autoscaling-group" {
		t.Error("gs.autoscalingGroupName should be my-autoscaling-group, but is ", aws.autoscalingGroupName)
	}

	if len(c) != 1 {
		t.Error("Expected ShutdownManager StartShutdown to be called once, got", len(c))
	}
}

func TestOtherInstance(t *testing.T) {
	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"my-lifecycle-hook"}`

	aws := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-752c5f8a",
	})
	aws.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})

	if aws.handleMessage(msg) {
		t.Error("Should detect different instance.")
	}
}

func TestOtherHook(t *testing.T) {
	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_TERMINATING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"other-lifecycle-hook"}`

	aws := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	aws.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})

	if aws.handleMessage(msg) {
		t.Error("Should detect different lifecycle hook.")
	}
}

func TestNonJsonMessage(t *testing.T) {
	msg := "message"

	aws := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	aws.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})

	if aws.handleMessage(msg) {
		t.Error("Should detect invalid json.")
	}
}

func TestOtherTransition(t *testing.T) {
	msg := `{"AutoScalingGroupName":"my-autoscaling-group","Service":"AWS Auto Scaling","LifecycleTransition":"autoscaling:EC2_INSTANCE_LAUNCHING","LifecycleActionToken":"my-lifecycle-token","EC2InstanceId":"i-1db84ae3","LifecycleHookName":"my-lifecycle-hook"}`

	aws := NewAwsManager(&AwsManagerConfig{
		LifecycleHookName: "my-lifecycle-hook",
		InstanceId:        "i-1db84ae3",
	})
	aws.gs = GSFunc(func(sm gracefulshutdown.ShutdownManager) {})

	if aws.handleMessage(msg) {
		t.Error("Should detect instance is not terminating.")
	}
}
