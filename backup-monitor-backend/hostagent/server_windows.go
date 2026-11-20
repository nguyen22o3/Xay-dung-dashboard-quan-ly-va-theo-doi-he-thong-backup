package hostagent

import (
	"context"
	"errors"
)

func Serve(context.Context, string, string, int) error {
	return errors.New("local host agent is supported only on Linux")
}
