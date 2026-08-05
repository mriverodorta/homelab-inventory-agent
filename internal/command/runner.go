package command

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type Exec struct {
	MaxOutput int
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (writer *boundedBuffer) Write(body []byte) (int, error) {
	if writer.buffer.Len()+len(body) > writer.limit {
		return 0, errors.New("command output exceeds the size limit")
	}
	return writer.buffer.Write(body)
}

func (runner Exec) Run(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	if runner.MaxOutput <= 0 {
		return nil, errors.New("command output limit is required")
	}
	process := exec.CommandContext(ctx, name, arguments...)
	output := &boundedBuffer{limit: runner.MaxOutput}
	process.Stdout = output
	process.Stderr = output
	err := process.Run()
	return append([]byte(nil), output.buffer.Bytes()...), err
}
