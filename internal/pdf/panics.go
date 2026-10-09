package pdf

import (
	"fmt"
	"runtime/debug"
	"sync"
)

// workerPanic carries a panic out of a ParallelFor worker together with the
// worker's stack, which is lost once the panic is re-raised elsewhere.
type workerPanic struct {
	value any
	stack []byte
}

func (p workerPanic) String() string { return fmt.Sprintf("%v\n%s", p.value, p.stack) }

// ParallelFor calls fn(i) for every i in [0, n) on up to workers goroutines
// and returns once all calls finish. A panic in fn would otherwise kill the
// process from a goroutine no caller can recover; instead the first one is
// re-raised in the calling goroutine after every worker has stopped, where
// RecoverAsError at the entry point turns it into an error.
func ParallelFor(n, workers int, fn func(i int)) {
	workers = min(workers, n)
	if workers < 1 {
		return
	}
	var (
		wg    sync.WaitGroup
		once  sync.Once
		first *workerPanic
	)
	jobs := make(chan int)
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				func() {
					defer func() {
						if r := recover(); r != nil {
							once.Do(func() { first = &workerPanic{r, debug.Stack()} })
						}
					}()
					fn(i)
				}()
			}
		}()
	}
	for i := range n {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		panic(*first)
	}
}

// RecoverAsError converts a panic in the deferring function into an
// ErrInternal-wrapped error stored in *err. Deferred at the public entry
// points so a defect triggered by one hostile input surfaces as that call's
// error rather than crashing the caller's process.
func RecoverAsError(err *error) {
	r := recover()
	if r == nil {
		return
	}
	if wp, ok := r.(workerPanic); ok {
		*err = fmt.Errorf("%w: %v", ErrInternal, wp)
		return
	}
	*err = fmt.Errorf("%w: %v\n%s", ErrInternal, r, debug.Stack())
}
