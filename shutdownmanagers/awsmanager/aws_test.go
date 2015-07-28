package awsmanager

import (
	"testing"
	"time"

	"github.com/Zemanta/gracefulshutdown"
)

type GSFunc func(sm gracefulshutdown.ShutdownManager)

func (f GSFunc) StartShutdown(sm gracefulshutdown.ShutdownManager) {
	f(sm)
}

func (f GSFunc) ReportError(err error) {

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

	time.Sleep(time.Millisecond)

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
