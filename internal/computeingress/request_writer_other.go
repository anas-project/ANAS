//go:build !linux || (!amd64 && !arm64)

package computeingress

func openRequestDirectory(string) (requestDirectory, error) {
	return nil, ErrRequestWriterUnavailable
}
