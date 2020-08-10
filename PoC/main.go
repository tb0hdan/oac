package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

func NewDR(runnerUUID string) *DockerRunner{
	dockerBinary, lookErr := exec.LookPath("docker")
	if lookErr != nil {
		panic(lookErr)
	}

	id, err := uuid.NewRandom()
	if err != nil {
		panic(err)
	}

	return &DockerRunner{
		DockerBinary: dockerBinary,
		Volume: id.String(),
		RunnerUUID: runnerUUID,
	}
}

type DockerRunner struct {
	DockerBinary string
	Volume string
	RunnerUUID string
}

func (dr *DockerRunner) StartRunnerContainer(ctx context.Context) *exec.Cmd{
	return exec.CommandContext(ctx, dr.DockerBinary, "run", "-d",
		"--network=none", "--memory=128m", "--memory-swap=128m",
		"--read-only",
		"-v", fmt.Sprintf("%s:/data:ro", dr.Volume),
		"alpine", "sh",
		"-c", "while :; do sleep 1; done")
}

func (dr *DockerRunner) StartTaskContainer(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, dr.DockerBinary, "run",
		"-v", fmt.Sprintf("%s:/data", dr.Volume),
		"oac-task", "/oac-downloader", dr.RunnerUUID,
		)
}

func (dr *DockerRunner) StopContainer(ctx context.Context, containerID string) *exec.Cmd{
	return exec.CommandContext(ctx, dr.DockerBinary, "stop",
	"-t", "1", containerID)

}

func (dr *DockerRunner) GetContainerOutput(cmd *exec.Cmd) string {
	out, err := cmd.StdoutPipe()

	if err != nil {
		panic(err)
	}

	err = cmd.Start()
	if err != nil {
		panic(err)
	}

	output, err := ioutil.ReadAll(out)
	if err != nil {
		panic(err)
	}

	err = cmd.Wait()

	if err != nil {
		panic(err)
	}

	return string(output)
}

func main() {
	dr := NewDR("da8f91bf-f8f6-42fb-a9db-cc25c0e564d8")

	cmd := dr.StartRunnerContainer(context.Background())
	out := dr.GetContainerOutput(cmd)
	containerID := strings.TrimRight(out, "\n")
	time.Sleep(3 * time.Second)
	fmt.Printf("`%s`\n", containerID)
	cmd = dr.StopContainer(context.Background(), containerID)
	out = dr.GetContainerOutput(cmd)
	fmt.Println(out)
}
