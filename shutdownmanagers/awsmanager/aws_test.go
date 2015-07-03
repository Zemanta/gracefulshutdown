package awsmanager

import (
	"testing"
)

func TestCorrectShutdownMessage(t *testing.T) {
	msg := `{"LifecycleHookName":"my-lifecycle-hook","EC2InstanceId":"i-1db84ae3","LifecycleActionToken":"my-lifecycle-token","AutoScalingGroupName":"my-autoscaling-group"}`

	aws := NewAwsManager(nil, "", "my-lifecycle-hook")
	aws.instanceId = "i-1db84ae3"

	if !aws.isMyShutdownMessage(msg) {
		t.Error("Should be correct shutdown message.")
	}

	if aws.lifecycleActionToken != "my-lifecycle-token" {
		t.Error("gs.lifecycleActionToken should be my-lifecycle-token, but is ", aws.lifecycleActionToken)
	}

	if aws.autoscalingGroupName != "my-autoscaling-group" {
		t.Error("gs.autoscalingGroupName should be my-autoscaling-group, but is ", aws.autoscalingGroupName)
	}
}

func TestOtherInstance(t *testing.T) {
	msg := `{"LifecycleHookName":"my-lifecycle-hook","EC2InstanceId":"i-1db84ae3","LifecycleActionToken":"my-lifecycle-token"}`

	aws := NewAwsManager(nil, "", "my-lifecycle-hook")
	aws.instanceId = "i-752c5f8a"

	if aws.isMyShutdownMessage(msg) {
		t.Error("Should detect different instance.")
	}
}

func TestOtherHook(t *testing.T) {
	msg := `{"LifecycleHookName":"other-lifecycle-hook","EC2InstanceId":"i-1db84ae3","LifecycleActionToken":"my-lifecycle-token"}`

	aws := NewAwsManager(nil, "", "my-lifecycle-hook")
	aws.instanceId = "i-1db84ae3"

	if aws.isMyShutdownMessage(msg) {
		t.Error("Should detect different lifecycle hook.")
	}
}

func TestNonJsonMessage(t *testing.T) {
	msg := "message"

	aws := NewAwsManager(nil, "", "my-lifecycle-hook")

	if aws.isMyShutdownMessage(msg) {
		t.Error("Should detect invalid json.")
	}
}
