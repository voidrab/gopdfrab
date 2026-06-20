// PreflightBatch drives Apache PDFBox's Preflight validator over a directory
// of PDFs inside a single JVM, for the amortized batch-throughput metric
// (complementing the per-file cold `java -jar preflight-app.jar <file>`
// invocation, which is dominated by JVM startup).
//
// preflight-app.jar ships its own "batch" mode (see its usage banner), but it
// writes one XML result file per PDF to disk and gave no usable output when
// exercised against this project's corpora; this driver calls the same
// PreflightParser.validate(File) API directly and is easier to control.
//
// Build:
//   javac -classpath tools/pdfbox/preflight-app.jar -d tools/pdfbox \
//       tools/pdfbox/PreflightBatch.java
// Run:
//   java -classpath tools/pdfbox/preflight-app.jar:tools/pdfbox PreflightBatch <dir>
//
// Output: one CSV row per file (path,size_bytes,nanos,valid,err) on stdout,
// then a "#summary ..." line on stderr with totals, throughput, and peak RSS
// (read from /proc/self/status VmHWM, Linux-only — mirrors how
// cmd/gopdfrab-bench self-reports memory via getrusage).
import java.io.File;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;
import java.util.logging.Level;
import java.util.logging.Logger;

import org.apache.pdfbox.preflight.ValidationResult;
import org.apache.pdfbox.preflight.parser.PreflightParser;

public class PreflightBatch {
    public static void main(String[] args) throws Exception {
        if (args.length < 1) {
            System.err.println("usage: PreflightBatch <dir>...");
            System.exit(2);
        }

        // PDFBox logs a WARNING per ICC-profile quirk on many real-world
        // files; that's expected noise for this corpus and would otherwise
        // drown out the CSV/summary output.
        Logger.getLogger("").setLevel(Level.SEVERE);

        // Accepts multiple roots (e.g. two separate corpus directories) so
        // callers don't need a shared parent directory that might pull in
        // unrelated files.
        List<File> files = new ArrayList<>();
        for (String arg : args) {
            File root = new File(arg);
            try (var stream = Files.walk(root.toPath())) {
                stream.map(Path::toFile)
                      .filter(f -> f.getName().toLowerCase().endsWith(".pdf"))
                      .forEach(files::add);
            }
        }

        int valid = 0, invalid = 0, errors = 0;
        long totalNanos = 0, totalBytes = 0;
        System.out.println("path,size_bytes,nanos,valid,err");
        for (File f : files) {
            long size = f.length();
            long start = System.nanoTime();
            String err = "";
            boolean isValid = false;
            try {
                ValidationResult result = PreflightParser.validate(f);
                isValid = result.isValid();
            } catch (Exception e) {
                err = e.getClass().getSimpleName();
            }
            long nanos = System.nanoTime() - start;
            totalNanos += nanos;
            totalBytes += size;
            if (!err.isEmpty()) {
                errors++;
            } else if (isValid) {
                valid++;
            } else {
                invalid++;
            }
            System.out.println(f.getPath() + "," + size + "," + nanos + "," + isValid + "," + err);
        }

        double secs = totalNanos / 1e9;
        double filesPerSec = secs > 0 ? files.size() / secs : 0;
        double mbPerSec = secs > 0 ? (totalBytes / 1048576.0) / secs : 0;
        System.err.printf(
            "#summary {\"tool\":\"pdfbox-preflight\",\"mode\":\"batch\",\"files\":%d,\"valid\":%d,"
                + "\"invalid\":%d,\"errors\":%d,\"total_bytes\":%d,\"total_nanos\":%d,"
                + "\"files_per_sec\":%.4f,\"mb_per_sec\":%.4f,\"max_rss_kb\":%d}%n",
            files.size(), valid, invalid, errors, totalBytes, totalNanos,
            filesPerSec, mbPerSec, maxRSSKB());
    }

    // maxRSSKB reads the JVM process's peak resident set size from
    // /proc/self/status (VmHWM), the Linux equivalent of the Go runner's
    // getrusage-based reading. Returns -1 if unavailable (e.g. non-Linux).
    private static long maxRSSKB() {
        try {
            for (String line : Files.readAllLines(Path.of("/proc/self/status"))) {
                if (line.startsWith("VmHWM:")) {
                    String digits = line.replaceAll("[^0-9]", "");
                    return Long.parseLong(digits);
                }
            }
        } catch (Exception e) {
            // ignore: not on Linux or /proc unavailable
        }
        return -1;
    }
}
