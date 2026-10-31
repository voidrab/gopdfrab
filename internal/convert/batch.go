package convert

import (
	"context"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// ConvertAll opens, converts, and closes a batch of files concurrently.
func ConvertAll(paths []string, p *pdf.Profile, o Options) ([]pdf.FileResult[ConvertResult], error) {
	return ConvertAllContext(context.Background(), paths, p, o)
}

// ConvertAllContext is ConvertAll honouring ctx cancellation: a cancelled ctx
// stops dispatching further files and records ctx.Err() for those not started.
// It holds every result resident and does not close them; the caller owns
// Close on each (large outputs spill to a temp file). ConvertEach streams and
// auto-closes instead when the batch is too large to keep in memory at once.
func ConvertAllContext(ctx context.Context, paths []string, p *pdf.Profile, o Options) ([]pdf.FileResult[ConvertResult], error) {
	results := make([]pdf.FileResult[ConvertResult], len(paths))
	convertEach(ctx, paths, p, o, func(i int, fr pdf.FileResult[ConvertResult]) error {
		results[i] = fr
		return nil
	})
	return results, nil
}

// ConvertEach opens, converts, and closes a batch of files concurrently,
// invoking fn on each result as it completes rather than retaining them all, so
// a large batch need not hold every output in memory at once. Each result is
// closed after its fn returns, so fn must not retain it (copy out what it needs).
func ConvertEach(paths []string, p *pdf.Profile, o Options, fn func(pdf.FileResult[ConvertResult]) error) error {
	return ConvertEachContext(context.Background(), paths, p, o, fn)
}

// ConvertEachContext is ConvertEach honouring ctx cancellation. fn is called
// once per file, serialized (never concurrently) but in completion order rather
// than the order of paths; the FileResult's Path identifies the file. If fn
// returns an error, no further files are dispatched or delivered and that error
// is returned. A cancelled ctx delivers ctx.Err() for files not yet started.
func ConvertEachContext(ctx context.Context, paths []string, p *pdf.Profile, o Options, fn func(pdf.FileResult[ConvertResult]) error) error {
	return convertEach(ctx, paths, p, o, func(_ int, fr pdf.FileResult[ConvertResult]) error {
		// The result is not retained past fn, so release its output (dropping a
		// spill temp file) as soon as fn returns.
		defer fr.Result.Close()
		return fn(fr)
	})
}

// convertEach is the shared worker engine behind ConvertAllContext and
// ConvertEachContext. It converts paths across o.workers() goroutines and calls
// sink(index, result) for each, serialized under a mutex so sink need not be
// concurrency-safe. The first sink error stops dispatch and delivery and is
// returned.
func convertEach(ctx context.Context, paths []string, p *pdf.Profile, o Options, sink func(int, pdf.FileResult[ConvertResult]) error) error {
	if len(paths) == 0 {
		return nil
	}
	workers := min(o.workers(), len(paths))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex
	var firstErr error
	deliver := func(i int, fr pdf.FileResult[ConvertResult]) {
		mu.Lock()
		defer mu.Unlock()
		if firstErr != nil {
			// Never reaches the sink, so nothing else will close it.
			fr.Result.Close()
			return
		}
		if err := sink(i, fr); err != nil {
			firstErr = err
			cancel()
		}
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				deliver(i, convertFile(ctx, paths[i], p, o))
			}
		}()
	}
	for i := range paths {
		if err := ctx.Err(); err != nil {
			deliver(i, pdf.FileResult[ConvertResult]{Path: paths[i], Err: err})
			continue
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return firstErr
}

func convertFile(ctx context.Context, path string, p *pdf.Profile, o Options) pdf.FileResult[ConvertResult] {
	cr, err := ConvertContext(ctx, path, p, o)
	return pdf.FileResult[ConvertResult]{Path: path, Result: cr, Err: err}
}
