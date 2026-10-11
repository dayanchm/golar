package connection_test

import (
	"github.com/dayanchm/golar/database/connection"
	"github.com/dayanchm/golar/database/drivers/sqlite"
	"sync"
	"testing"
)

func TestDriverRegistryConcurrentDuplicates(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := connection.RegisterDriver(sqlite.Driver{}); err == nil {
				t.Error("duplicate driver accepted")
			}
		}()
	}
	wg.Wait()
}
