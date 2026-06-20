package api

import (
	"context"
	"sync"
)

// osImageFake is the package-global fake state for the two ADR-0040 store
// methods (ListOSImages + BackfillNodeImages). Handler tests set fixtures
// and assert recorded calls; the real SQL is covered in internal/store.
var osImageFake = struct {
	mu         sync.Mutex
	images     []OSImage   // fixture returned by ListOSImages
	backfilled []NodeImage // recorded BackfillNodeImages input
	matched    int         // value returned by BackfillNodeImages
	updated    int         // value returned by BackfillNodeImages
}{}

func resetOSImageFake() {
	osImageFake.mu.Lock()
	defer osImageFake.mu.Unlock()
	osImageFake.images = nil
	osImageFake.backfilled = nil
	osImageFake.matched = 0
	osImageFake.updated = 0
}

func (m *memStore) BackfillNodeImages(_ context.Context, images []NodeImage) (int, int, error) {
	osImageFake.mu.Lock()
	defer osImageFake.mu.Unlock()
	osImageFake.backfilled = append(osImageFake.backfilled, images...)
	return osImageFake.matched, osImageFake.updated, nil
}

func (m *memStore) ListOSImages(_ context.Context) ([]OSImage, error) {
	osImageFake.mu.Lock()
	defer osImageFake.mu.Unlock()
	return osImageFake.images, nil
}
