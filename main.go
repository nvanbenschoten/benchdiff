package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nvanbenschoten/benchdiff/google"
	"github.com/nvanbenschoten/benchdiff/ui"
	"github.com/pkg/errors"
	"github.com/spf13/pflag"
	"golang.org/x/perf/benchstat"
)

const usage = `usage: benchdiff [--old <commit>] [--new <commit>] <pkgs>...`

const helpString = `benchdiff automates the process of running and comparing Go microbenchmarks
across code changes.

benchdiff runs all microbenchmarks in the specified packages against the old and
new commit. It then passes the benchmark output through benchstat to compute
statistics about the results.

By default, benchdiff outputs these results in a textual format. However, if the
--sheets flag is passed then it will upload the result to a Google Sheets
spreadsheet. To access this, users must have a Google service account. For
information, see https://cloud.google.com/iam/docs/service-accounts.

The Google service account must meet the following conditions:
1. The Google Sheets API must be enabled for the account's project
2. The Google Drive  API must be enabled for the account's project

When the --sheets flag is passed, benchdiff will search for a credentials file
containing the service account key using the GOOGLE_APPLICATION_CREDENTIALS
environment variable. See https://cloud.google.com/docs/authentication/production.

Options:
  -n, --new       <commit>  measure the difference between this commit and old (default HEAD)
  -o, --old       <commit>  measure the difference between this commit and new (default new~)
                            'lastmerge' selects the most recent merge commit.
  -r, --run       <regexp>  run only benchmarks matching regexp
  -c, --count     <n>       run tests and benchmarks n times (default 10)
  -d  --benchtime <d>       run each benchmark for duration d (default 1s)
      --cpuprofile          record and write cpu profiles
      --memprofile          record and write allocation profiles
      --mutexprofile        record and write mutex contention profiles
  -t, --threshold <n>       exit with code 0 if all regressions are below threshold, else 1
  -p, --previous-run <time> time of previous run; skip running benches and just (re)process previous run
      --post-checkout       an optional command to run after checking out each branch to
                            configure the git repo so that 'go build' succeeds
  -b  --bazel               build the test binaries with bazel
  -s  --sort      <order>   sort output by 'delta' (largest first) or 'name'
      --csv                 output the results in a csv format
      --html                output the results in an HTML table
      --sheets              output the results to a new Google Sheets document
      --help                display this help

Example invocations:
  $ benchdiff --sheets ./pkg/...
  $ benchdiff --old=master~ --new=master --threshold=0.2 ./pkg/kv ./pkg/storage/...
  $ benchdiff --new=d1fbdb2 --run=Datum --count=2 --csv ./pkg/sql/...
  $ benchdiff --new=6299bd4 --sheets --post-checkout='dev generate go' ./pkg/workload/...`

// TODO: it's unclear whether G Suite Domain-wide Delegation is required for the
// Google service account. If it is, add the following requirement to the help
// text above.
//   3. G Suite Domain-wide Delegation must be enabled. See
//    https://developers.google.com/identity/protocols/OAuth2ServiceAccount#delegatingauthority.

type outputFmt int

const (
	_ outputFmt = iota
	// Output the benchmark comparison in a text format to stdout.
	//
	// Example:
	//   name         old time/op    new time/op    delta
	//   String-8       68.6ns ± 0%    68.2ns ± 0%   ~     (p=1.000 n=1+1)
	//   FromBytes-8    4.92ns ± 0%    4.97ns ± 0%   ~     (p=1.000 n=1+1)
	text
	// Output the benchmark comparison in a csv format to stdout.
	//
	// Example:
	//   name,old time/op (ns/op),±,new time/op (ns/op),±,delta,±
	//   String-8,6.82000E+01,0%,6.76000E+01,0%,~,(p=1.000 n=1+1)
	//   FromBytes-8,5.01000E+00,0%,4.95000E+00,0%,~,(p=1.000 n=1+1)
	csv
	// Output the benchmark comparison in an HTML format to stdout.
	//
	// Example:
	//   <table class='benchstat oldnew'>
	//   <tr class='configs'><th><th>old<th>new
	//   <tbody>
	//   <tr><th><th colspan='2' class='metric'>time/op<th>delta
	//   <tr class='unchanged'><td>String-8<td>70.1ns ± 0%<td>69.6ns ± 0%<td class='nodelta'>~<td class='note'>(p=1.000 n=1&#43;1)
	//   <tr class='unchanged'><td>FromBytes-8<td>5.42ns ± 0%<td>5.05ns ± 0%<td class='nodelta'>~<td class='note'>(p=1.000 n=1&#43;1)
	//   <tr><td>&nbsp;
	//   </tbody>
	//   </table>
	html
	// Output the benchmark comaprison in a Google Sheets format and print
	// the sheet's URL to stdout. When in this mode, the comparison is also
	// printed as text to stdout.
	//
	// Example:
	//   name         old time/op    new time/op    delta
	//   String-8       68.6ns ± 0%    68.2ns ± 0%   ~     (p=1.000 n=1+1)
	//   FromBytes-8    4.92ns ± 0%    4.97ns ± 0%   ~     (p=1.000 n=1+1)
	//
	//   generated sheet: https://docs.google.com/spreadsheets/...
	sheets
)

const timeFormat = "2006-01-02T15_04_05Z07:00"

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	var help, outCSV, outHTML, outSheets bool
	var oldRef, newRef, order, postChck, runPattern, benchTime, previousRun string
	var itersPerTest int
	var cpuProfile, memProfile, mutexProfile bool
	var threshold float64
	var useBazel bool

	pflag.Usage = func() { fmt.Fprintln(os.Stderr, usage) }
	pflag.BoolVarP(&help, "help", "h", false, "")
	pflag.BoolVarP(&outCSV, "csv", "", false, "")
	pflag.BoolVarP(&outHTML, "html", "", false, "")
	pflag.BoolVarP(&outSheets, "sheets", "", false, "")
	pflag.BoolVarP(&useBazel, "bazel", "b", false, "")
	pflag.StringVarP(&oldRef, "old", "o", "", "")
	pflag.StringVarP(&newRef, "new", "n", "", "")
	pflag.StringVarP(&order, "sort", "s", "delta", "")
	pflag.StringVarP(&postChck, "post-checkout", "", "", "")
	pflag.StringVarP(&runPattern, "run", "r", ".", "")
	pflag.IntVarP(&itersPerTest, "count", "c", 10, "")
	pflag.StringVarP(&benchTime, "benchtime", "d", "", "")
	pflag.BoolVarP(&cpuProfile, "cpuprofile", "", false, "")
	pflag.BoolVarP(&memProfile, "memprofile", "", false, "")
	pflag.BoolVarP(&mutexProfile, "mutexprofile", "", false, "")
	pflag.Float64VarP(&threshold, "threshold", "t", -1, "")
	pflag.StringVarP(&previousRun, "previous-run", "p", "", "")
	pflag.Parse()
	prArgs := pflag.Args()

	if help {
		return runHelp(ctx)
	}
	if len(prArgs) == 0 && previousRun == "" {
		return runHelp(ctx)
	}
	pkgFilter := prArgs
	sort.Strings(pkgFilter)

	// Parse the output format.
	var out outputFmt
	var srv *google.Service
	var err error
	switch {
	case outCSV:
		if outHTML {
			return errors.New("--csv and --html incompatible")
		} else if outSheets {
			return errors.New("--csv and --sheets incompatible")
		}
		out = csv
	case outHTML:
		if outSheets {
			return errors.New("--html and --sheets incompatible")
		}
		out = html
	case outSheets:
		out = sheets
		// Init the Google service ASAP to detect credential issues.
		if srv, err = google.New(ctx); err != nil {
			return err
		}
	default:
		out = text
	}

	// Parse the specified git refs.
	oldRef, newRef, err = parseGitRefs(oldRef, newRef)
	if err != nil {
		return err
	}
	oldSubject, err := subjectForRef(oldRef)
	if err != nil {
		return err
	}
	newSubject, err := subjectForRef(newRef)
	if err != nil {
		return err
	}

	// Build the benchmark suites.
	oldSuite := makeBenchSuite(oldRef, oldSubject, useBazel)
	newSuite := makeBenchSuite(newRef, newSubject, useBazel)
	defer oldSuite.close()
	defer newSuite.close()

	printHeader(os.Stdout, oldSuite, newSuite)

	if previousRun == "" {
		if err := buildBenches(ctx, pkgFilter, postChck, &oldSuite, &newSuite); err != nil {
			return err
		}

		// Run the benchmarks.
		tests := oldSuite.intersectTests(&newSuite)
		err = runCmpBenches(
			ctx, &oldSuite, &newSuite, tests.sorted(), runPattern,
			benchTime, cpuProfile, memProfile, mutexProfile, itersPerTest,
		)
		if err != nil {
			return err
		}
	} else {
		// Find output files for the given run.
		t, err := time.Parse(timeFormat, previousRun)
		if err != nil {
			return err
		}

		// Install existing artifacts into benchSuites.
		oldSuite.artDir = testArtifactsDir(oldSuite.ref)
		oldSuite.outFile, err = os.Open(oldSuite.getOutputFile(t))
		if err != nil {
			return err
		}
		newSuite.artDir = testArtifactsDir(newSuite.ref)
		newSuite.outFile, err = os.Open(newSuite.getOutputFile(t))
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "Found previous run; old=%s, new=%s\n", oldSuite.outFile.Name(), newSuite.outFile.Name())
	}
	// Process the benchmark output.
	res, err := processBenchOutput(ctx, &oldSuite, &newSuite, order == "name", out, pkgFilter, srv)
	if err != nil {
		return err
	}
	logProfileLocations(&oldSuite, &newSuite, cpuProfile, memProfile, mutexProfile)

	// Determine whether any tests exceeded the allowable regression threshold.
	return checkPassing(threshold, res)
}

func runHelp(ctx context.Context) error {
	fmt.Fprintln(os.Stderr, usage)
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, helpString)
	return nil
}

func parseGitRefs(oldRef, newRef string) (string, string, error) {
	var err error
	if newRef == "" {
		newRef, err = getCurRef()
		if err != nil {
			return "", "", err
		}
	} else {
		newRef, err = getRefAsSHA(newRef)
		if err != nil {
			return "", "", err
		}
	}
	newRef = shortenRef(newRef)
	if ok, err := checkValidRef(newRef); err != nil {
		return "", "", err
	} else if !ok {
		return "", "", errors.Errorf("invalid git ref %q", newRef)
	}

	if oldRef == "" {
		oldRef, err = getPrevRef(newRef)
		if err != nil {
			return "", "", err
		}
	} else if oldRef == "lastmerge" {
		oldRef, err = capture("git", "log", "-n", "1", "--merges", "--format=%H", newRef)
	} else {
		oldRef, err = getRefAsSHA(oldRef)
		if err != nil {
			return "", "", err
		}
	}
	oldRef = shortenRef(oldRef)
	if ok, err := checkValidRef(oldRef); err != nil {
		return "", "", err
	} else if !ok {
		return "", "", errors.Errorf("invalid git ref %q", oldRef)
	}

	return oldRef, newRef, nil
}

func buildBenches(ctx context.Context, pkgFilter []string, postChck string, bss ...*benchSuite) error {
	// Get the current branch so we can revert to it after, if possible.
	if ref, ok, err := getCurSymbolicRef(); err != nil {
		return err
	} else if ok {
		defer checkoutRef(ref, "")
	}
	now := time.Now() // used to uniquely name artifact files
	for _, bs := range bss {
		if err := bs.build(pkgFilter, postChck, now); err != nil {
			return err
		}
	}
	return nil
}

func runCmpBenches(
	ctx context.Context,
	bs1, bs2 *benchSuite,
	tests []string,
	runPattern, benchTime string,
	cpuProfile, memProfile, mutexProfile bool,
	itersPerTest int,
) error {
	var spinner ui.Spinner
	spinner.Start(os.Stderr, "running benchmarks:")
	defer spinner.Stop()
	for i, t := range tests {
		pkg := testBinToPkg(t)
		for j := 0; j < itersPerTest; j++ {
			pkgFrac := ui.Fraction(i+1, len(tests))
			iterFrac := ui.Fraction(j+1, itersPerTest)
			progress := fmt.Sprintf(" pkg=%s iter=%s %s", pkgFrac, iterFrac, pkg)
			spinner.Update(progress)

			// Interleave test suite runs instead of using -count=itersPerTest. The
			// idea is that this reduces the chance that we pick up external noise
			// with a time correlation.
			if err := runSingleBench(bs1, t, runPattern, benchTime, cpuProfile, memProfile, mutexProfile); err != nil {
				return err
			}
			if err := runSingleBench(bs2, t, runPattern, benchTime, cpuProfile, memProfile, mutexProfile); err != nil {
				return err
			}
		}
	}
	return nil
}

func runSingleBench(
	bs *benchSuite, test, runPattern, benchTime string, cpuProfile, memProfile, mutexProfile bool,
) error {
	bin := bs.getTestBinary(test)

	// Determine whether the binary has a --logtostderr flag. Use CombinedOutput
	// and ignore the error because --help creates a failed error status. If there
	// is a real error we'll hit it below.
	cmd := exec.Command(bin, "--help")
	out, _ := cmd.CombinedOutput()
	hasLogToStderr := bytes.Contains(out, []byte("logtostderr"))

	// Run the benchmark binary.
	args := []string{bin, "-test.run", "-", "-test.bench", runPattern, "-test.benchmem"}
	if benchTime != "" {
		args = append(args, "-test.benchtime", benchTime)
	}
	if cpuProfile {
		args = append(args, "-test.cpuprofile", bs.getCpuProfileFile())
	}
	if memProfile {
		// TODO(nvanbenschoten): consider passing -test.memprofilerate=1.
		args = append(args, "-test.memprofile", bs.getMemProfileFile())
	}
	if mutexProfile {
		args = append(args, "-test.mutexprofile", bs.getMutexProfileFile())
	}
	if hasLogToStderr {
		args = append(args, "--logtostderr", "NONE")
	}
	if err := spawnWith(os.Stdin, bs.outFile, bs.outFile, args...); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				// Assume exit code 1 corresponds to a benchmark failure.
				fmt.Fprintln(os.Stderr, "  saw one or more benchmark failures")
			} else {
				return errors.Wrapf(err, "error running %v: %s", args, exitErr.Stderr)
			}
		} else {
			return errors.Wrapf(err, "error running %v", args)
		}
	}
	return nil
}

func processBenchOutput(
	ctx context.Context,
	oldSuite, newSuite *benchSuite,
	byName bool, // instead of by delta reversed
	out outputFmt,
	pkgFilter []string,
	srv *google.Service,
) ([]*benchstat.Table, error) {
	// We're going to be reading the output files, so seek to the beginning.
	oldSuite.outFile.Seek(0, io.SeekStart)
	newSuite.outFile.Seek(0, io.SeekStart)

	// Compute the benchmark comparison results.
	var c benchstat.Collection
	c.Alpha = 0.05
	if byName {
		c.Order = benchstat.ByName
	} else {
		c.Order = benchstat.Reverse(benchstat.ByDelta) // best, first
	}
	if err := c.AddFile("old", oldSuite.outFile); err != nil {
		return nil, err
	}
	if err := c.AddFile("new", newSuite.outFile); err != nil {
		return nil, err
	}
	tables := c.Tables()

	// Output the results.
	switch out {
	case text:
		benchstat.FormatText(os.Stdout, tables)
	case csv:
		// If norange is true, suppress the range information for each data item.
		// If norange is false, insert a "±" in the appropriate columns of the header row.
		norange := false
		benchstat.FormatCSV(os.Stdout, tables, norange)
	case html:
		var buf bytes.Buffer
		benchstat.FormatHTML(&buf, tables)
		io.Copy(os.Stdout, &buf)
	case sheets:
		// When outputting a Google sheet, also output as text first.
		benchstat.FormatText(os.Stdout, tables)

		sheetName := fmt.Sprintf("benchdiff: %s (%s -> %s)",
			strings.Join(pkgFilter, " "), oldSuite.ref, newSuite.ref)
		url, err := srv.CreateSheet(ctx, sheetName, tables)
		if err != nil {
			return nil, err
		}
		fmt.Printf("\ngenerated sheet: %s\n", url)
	default:
		panic("unexpected")
	}
	return tables, nil
}

func logProfileLocations(
	bs1, bs2 *benchSuite, cpuProfile, memProfile, mutexProfile bool,
) {
	log := func(profType string) {
		fmt.Printf("\nwrote %s profiles to:\n  old=%s\n  new=%s\n",
			profType, bs1.getProfileFile(profType), bs2.getProfileFile(profType))
	}
	if cpuProfile {
		log("cpu")
	}
	if memProfile {
		log("mem")
	}
	if mutexProfile {
		log("mutex")
	}
}

func checkPassing(thresh float64, tables []*benchstat.Table) error {
	if thresh < 0 {
		return nil
	}
	threshPct := thresh * 100
	for _, table := range tables {
		for _, row := range table.Rows {
			worse := row.Change == -1
			exceededThresh := math.Abs(row.PctDelta) > threshPct
			if worse && exceededThresh {
				return errors.Errorf("%s regression in %s of %s exceeded threshold of %.2f%%",
					table.Metric, row.Benchmark, row.Delta, threshPct)
			}
		}
	}
	return nil
}

type benchSuite struct {
	ref       string
	subject   string // commit subject
	artDir    string
	outFile   *os.File
	binDir    string
	useBazel  bool
	testFiles fileSet
}
type fileSet map[string]struct{}

func makeBenchSuite(ref string, subject string, useBazel bool) benchSuite {
	return benchSuite{
		ref:       ref,
		subject:   subject,
		testFiles: make(fileSet),
		useBazel:  useBazel,
	}
}

func (bs *benchSuite) build(pkgFilter []string, postChck string, t time.Time) (err error) {
	if len(bs.testFiles) != 0 {
		panic("benchSuite already built")
	}

	// Create the artifacts directory: ./benchdiff/<ref>/artifacts
	bs.artDir = testArtifactsDir(bs.ref)
	if err = os.MkdirAll(bs.artDir, 0744); err != nil {
		return err
	}

	// Create output file: ./benchdiff/<ref>/artifacts/out.<time>
	outFileName := bs.getOutputFile(t)
	bs.outFile, err = os.OpenFile(outFileName, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}

	// Create the binary directory: ./benchdiff/<ref>/bin/<hash(pkgFilter)>
	bs.binDir = testBinDir(bs.ref, pkgFilter)
	if _, err = os.Stat(bs.binDir); err == nil {
		files, err := ioutil.ReadDir(bs.binDir)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.IsDir() {
				return errors.Errorf("unexpected directory %q", f.Name())
			}
			bs.testFiles[f.Name()] = struct{}{}
		}
		return nil
	} else if !os.IsNotExist(err) {
		return errors.Wrap(err, "looking for test directory")
	}
	if err := os.MkdirAll(bs.binDir, 0700); err != nil {
		return err
	}
	// If the binaries are not generated successfully, delete the bin directory
	// so we don't consider the build successful next time benchdiff runs.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(bs.binDir)
		}
	}()

	if err := checkoutRef(bs.ref, postChck); err != nil {
		return err
	}

	// Determine which packages to build.
	pkgs, err := expandPackages(pkgFilter)
	if err != nil {
		return err
	}

	var spinner ui.Spinner
	spinner.Start(os.Stderr, fmt.Sprintf("building benchmark binaries for %s: %.50s [bazel=%t]", bs.ref,
		bs.subject, bs.useBazel))
	defer spinner.Stop()
	for i, pkg := range pkgs {
		spinner.Update(ui.Fraction(i, len(pkgs)))
		if testBin, ok, err := buildTestBin(pkg, bs.binDir, bs.useBazel); err != nil {
			return err
		} else if ok {
			bs.testFiles[testBin] = struct{}{}
		}
		spinner.Update(ui.Fraction(i+1, len(pkgs)))
	}
	return nil
}

func (bs *benchSuite) close() {
	_ = bs.outFile.Close()
}

func (bs *benchSuite) getOutputFile(t time.Time) string {
	return filepath.Join(bs.artDir, "out."+t.Format(timeFormat))
}

func (bs *benchSuite) getCpuProfileFile() string   { return bs.getProfileFile("cpu") }
func (bs *benchSuite) getMemProfileFile() string   { return bs.getProfileFile("mem") }
func (bs *benchSuite) getMutexProfileFile() string { return bs.getProfileFile("mutex") }

func (bs *benchSuite) getProfileFile(profType string) string {
	return filepath.Join(bs.artDir, profType+".prof")
}

func (bs *benchSuite) getTestBinary(bin string) string {
	return filepath.Join(bs.binDir, bin)
}

func (bs *benchSuite) intersectTests(bs2 *benchSuite) fileSet {
	intersect := make(fileSet)
	for f := range bs.testFiles {
		if _, ok := bs2.testFiles[f]; ok {
			intersect[f] = struct{}{}
		}
	}
	return intersect
}

func (fs fileSet) sorted() []string {
	s := make([]string, 0, len(fs))
	for t := range fs {
		s = append(s, t)
	}
	sort.Strings(s)
	return s
}

func printHeader(w io.Writer, oldSuite, newSuite benchSuite) {
	fmt.Fprintf(w, "old:  %s %.50s\n", oldSuite.ref, oldSuite.subject)
	fmt.Fprintf(w, "new:  %s %.50s\n", newSuite.ref, newSuite.subject)
	fmt.Fprintf(w, "args: %s\n\n", strings.Join(func() []string {
		quoted := make([]string, 1+len(os.Args[1:]))
		quoted[0] = "benchdiff"
		for i, arg := range os.Args[1:] {
			quoted[1+i] = strconv.Quote(arg)
		}
		return quoted
	}(), " "))
}
