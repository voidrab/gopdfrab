// Command gopdfrab-bench drives the gopdfrab library for benchmarking
// against other PDF/A verifiers (veraPDF, PDFBox Preflight, a JS validator).
//
// Two modes:
//
//	single <file>   times one Open+Verify call, prints one CSV/JSON row.
//	batch  <dir>    walks dir for *.pdf, times Open+Verify per file, prints
//	                one row per file plus a trailing summary line, and
//	                reports the process's peak RSS (max RSS, via getrusage)
//	                so memory is captured even when the caller isn't
//	                wrapping the process in GNU time.
//
// Output is CSV by default (path,size_bytes,nanos,valid,err) with a
// "#summary" comment line at the end; pass -json for line-delimited JSON
// instead. This mirrors the walk+Open+Verify pattern in main/main.go.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	pdfrab "github.com/voidrab/gopdfrab"
)

type fileResult struct {
	Path   string `json:"path"`
	Size   int64  `json:"size_bytes"`
	Nanos  int64  `json:"nanos"`
	Valid  bool   `json:"valid"`
	Err    string `json:"err"`
	Issues int    `json:"issues"`
}

type summary struct {
	Tool        string  `json:"tool"`
	Mode        string  `json:"mode"`
	Files       int     `json:"files"`
	Valid       int     `json:"valid"`
	Invalid     int     `json:"invalid"`
	Errors      int     `json:"errors"`
	TotalBytes  int64   `json:"total_bytes"`
	TotalNanos  int64   `json:"total_nanos"`
	FilesPerSec float64 `json:"files_per_sec"`
	MBPerSec    float64 `json:"mb_per_sec"`
	MaxRSSKB    int64   `json:"max_rss_kb"`
}

func main() {
	mode := flag.String("mode", "single", "single|batch")
	jsonOut := flag.Bool("json", false, "emit line-delimited JSON instead of CSV")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatalf("usage: gopdfrab-bench -mode=single|batch [-json] <path>...")
	}

	var paths []string
	switch *mode {
	case "single":
		if flag.NArg() != 1 {
			log.Fatalf("-mode=single takes exactly one file path")
		}
		paths = []string{flag.Arg(0)}
	case "batch":
		for _, target := range flag.Args() {
			err := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdf") {
					paths = append(paths, path)
				}
				return nil
			})
			if err != nil {
				log.Fatalf("walk %s: %v", target, err)
			}
		}
	default:
		log.Fatalf("unknown -mode=%q (want single|batch)", *mode)
	}

	wallStart := time.Now()
	results := runAll(paths)
	wallElapsed := time.Since(wallStart)

	csvW := csv.NewWriter(os.Stdout)
	if !*jsonOut {
		_ = csvW.Write([]string{"path", "size_bytes", "nanos", "valid", "err", "issues"})
	}

	sum := summary{Tool: "gopdfrab", Mode: *mode}
	for _, res := range results {
		sum.Files++
		sum.TotalBytes += res.Size
		sum.TotalNanos += res.Nanos
		switch {
		case res.Err != "":
			sum.Errors++
		case res.Valid:
			sum.Valid++
		default:
			sum.Invalid++
		}

		if *jsonOut {
			b, _ := json.Marshal(res)
			fmt.Println(string(b))
		} else {
			_ = csvW.Write([]string{
				res.Path,
				strconv.FormatInt(res.Size, 10),
				strconv.FormatInt(res.Nanos, 10),
				strconv.FormatBool(res.Valid),
				res.Err,
				strconv.Itoa(res.Issues),
			})
		}
	}
	if !*jsonOut {
		csvW.Flush()
	}

	if wallElapsed > 0 {
		secs := wallElapsed.Seconds()
		sum.FilesPerSec = float64(sum.Files) / secs
		sum.MBPerSec = (float64(sum.TotalBytes) / (1024 * 1024)) / secs
	}
	sum.MaxRSSKB = maxRSSKB()

	b, _ := json.Marshal(sum)
	fmt.Fprintf(os.Stderr, "#summary %s\n", string(b))
}

// runAll verifies every path through a worker pool bounded to NumCPU,
// returning results in the same order as paths regardless of which worker
// finished first.
func runAll(paths []string) []fileResult {
	results := make([]fileResult, len(paths))

	workers := min(runtime.NumCPU(), len(paths))
	if workers < 1 {
		return results
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i] = runOne(paths[i])
			}
		}()
	}
	for i := range paths {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return results
}

func runOne(path string) fileResult {
	info, statErr := os.Stat(path)
	var size int64
	if statErr == nil {
		size = info.Size()
	}

	start := time.Now()
	doc, err := pdfrab.Open(path)
	if err != nil {
		return fileResult{Path: path, Size: size, Nanos: time.Since(start).Nanoseconds(), Err: err.Error()}
	}
	res, err := doc.Verify(pdfrab.PDFA_1B)
	elapsed := time.Since(start)
	doc.Close()
	if err != nil {
		return fileResult{Path: path, Size: size, Nanos: elapsed.Nanoseconds(), Err: err.Error()}
	}
	return fileResult{Path: path, Size: size, Nanos: elapsed.Nanoseconds(), Valid: res.Valid, Issues: res.Count()}
}

// maxRSSKB reports the process's peak resident set size in KB via getrusage,
// so memory consumption is captured even when the caller doesn't wrap this
// process in `/usr/bin/time -v`. On Linux, ru_maxrss is already in KB.
func maxRSSKB() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return -1
	}
	return int64(ru.Maxrss)
}
