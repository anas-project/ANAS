//go:build !linux

package incusingresshost

import (
	"context"
	"errors"
)

func readNFTTableWithKernelIndices(context.Context, string, func() (nftTable, error)) (nftTable, error) {
	return nftTable{}, errors.New("kernel nft interface evidence requires Linux")
}
