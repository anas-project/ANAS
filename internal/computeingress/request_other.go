//go:build !linux || (!amd64 && !arm64)

package computeingress

import (
	"fmt"
	"os"
)

func ReadRequest(directory *os.File, name string) (Request, error) {
	return Request{}, fmt.Errorf("consumer HTTP request-directory reads require Linux amd64 or arm64")
}
