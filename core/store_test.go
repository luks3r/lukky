package core_test

import (
	"testing"

	"github.com/luks3r/lukky/core"
	"github.com/luks3r/lukky/storage/storagetest"
)

func TestMemoryStorageContract(t *testing.T) {
	storagetest.Run(t, func(*testing.T) core.Storage { return core.NewMemoryStorage() })
}
