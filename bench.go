package main

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

const bazelRunScript = `#!/bin/bash
SCRIPT_PATH="$(realpath "$0")"
BAZEL_DIR="${SCRIPT_PATH}.bazel"
RUNFILES_DIR="${BAZEL_DIR}/%[1]s.runfiles"
"${BAZEL_DIR}/%[1]s" "$@"
`

// expandPackages expands the package filter into all of the packages that it
// references using `go list`.
func expandPackages(pkgFilter []string) ([]string, error) {
	args := []string{"go", "list"}
	args = append(args, pkgFilter...)
	pkgs, err := capture(args...)
	if err != nil {
		return nil, errors.Wrap(err, "expanding packages")
	}
	return strings.Split(pkgs, "\n"), nil
}

// testDir returns the directory to store benchdiff artifacts and binaries for
// specified git ref.
func testDir(ref string) string {
	return filepath.Join("benchdiff", ref)
}

// testArtifactsDir returns the directory to store benchdiff artifacts for
// specified git ref.
func testArtifactsDir(ref string) string {
	return filepath.Join(testDir(ref), "artifacts")
}

func hash(s []string) string {
	h := fnv.New32a()
	for _, ss := range s {
		h.Write([]byte(ss))
	}
	u := h.Sum32()
	return strconv.Itoa(int(u))
}

// testArtifactsDir returns the directory to store benchdiff binaries for
// specified git ref.
func testBinDir(ref string, pkgFilter []string) string {
	return filepath.Join(testDir(ref), "bin", hash(pkgFilter))
}

// pkgToTestBin translates a Go package name into a test binary name.
func pkgToTestBin(pkg string) string {
	// Strip github.com prefix.
	f := strings.TrimPrefix(pkg, "github.com")
	// Turn forward-slashes into underscores.
	f = strings.ReplaceAll(f, "/", "_")
	// Trim leading underscores.
	return strings.TrimLeft(f, "_")
}

// testBinToPkg translates a test binary name to a Go package name. This
// tranlation does not round-trip, but comes close enough.
func testBinToPkg(bin string) string {
	return strings.ReplaceAll(bin, "_", "/")
}

// buildTestBinWithGo builds a test binary, using Go directly, for the specified
// package and moves it to the destination directory if successful.
func buildTestBinWithGo(pkg, dst string) (string, bool, error) {
	dstFile := pkgToTestBin(pkg) // cockroachdb_cockroach_pkg_util_log
	// Capture to silence warnings from pkgs with no test files.
	if _, err := capture("go", "test", "-c", "-o", dstFile, pkg); err != nil {
		return "", false, errors.Wrap(err, "building test binary")
	}

	// If there were no tests in the package, no file will have been created.
	if _, err := os.Stat(dstFile); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errors.Wrap(err, "looking for test binary")
	}
	if err := spawn("mv", dstFile, filepath.Join(dst, dstFile)); err != nil {
		return "", false, errors.Wrap(err, "moving test binary")
	}
	return dstFile, true, nil
}

// buildTestBinWithBazel builds a test binary, using Bazel, for the specified
// package. It creates an executable script inplace of a binary that will invoke
// the test binary with the correct runfiles, stored in a `<dst>.bazel`
// directory alongside it.
func buildTestBinWithBazel(pkg, dst string) (string, bool, error) {
	dstBin := pkgToTestBin(pkg) // cockroachdb_cockroach_pkg_util_log
	dstBazelDir := filepath.Join(dst, dstBin+".bazel")

	relPkg := strings.TrimPrefix(pkg, "github.com/cockroachdb/cockroach/")
	pathList := strings.Split(relPkg, string(filepath.Separator)) // ['pkg','util','log']
	last := pathList[len(pathList)-1]                             // 'log'
	// `bazel build //pkg/util/log:log_test`.
	if _, err := capture("bazel", "build", "//"+relPkg+":"+last+"_test"); err != nil {
		return "", false, errors.Wrap(err, "building test binary")
	}

	// `_bazel/bin/pkg/util/log/log_test_`.
	outDir := append([]string{"_bazel", "bin"}, pathList...)
	outDir = append(outDir, last+"_test_")

	// `_bazel/bin/pkg/util/log/log_test_/log_test`.
	srcBin := filepath.Join(filepath.Join(outDir...), last+"_test")
	// `_bazel/bin/pkg/util/log/log_test_/log_test.runfiles`.
	srcRunfilesDir := filepath.Join(filepath.Join(outDir...), filepath.Base(srcBin)+".runfiles")

	// If there were no tests in the package, no test binary file will have been
	// created.
	if _, err := os.Stat(srcBin); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, errors.Wrap(err, "looking for test binary")
	}
	if err := os.Mkdir(dstBazelDir, 0755); err != nil {
		return "", false, errors.Wrap(err, "creating bazel binary directory")
	}
	if err := spawn("cp", "-rL", srcBin, srcRunfilesDir, dstBazelDir); err != nil {
		return "", false, errors.Wrap(err, "copying binary and bazel runfiles")
	}
	runScript := fmt.Sprintf(bazelRunScript, filepath.Base(srcBin))
	if err := writeExecutableScript(runScript, filepath.Join(dst, dstBin)); err != nil {
		return "", false, errors.Wrap(err, "writing bazel binary script")
	}
	return dstBin, true, nil
}

func writeExecutableScript(script, path string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(script)
	return err
}
