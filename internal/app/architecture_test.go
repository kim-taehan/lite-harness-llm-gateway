package app

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// 모듈 경계 검사(설계 3장). Go 컴파일러는 순환 import 와 internal/ 밖 참조를 막아 주지만
// 모듈 사이 의존 방향(admin → inference → auth·limits → core)은 모른다. 그것을 여기서 막는다.

const modulePath = "llm-gateway"

// 모듈마다 가져올 수 있는 다른 모듈. 같은 모듈의 하위 패키지는 언제나 된다
var allowedDeps = map[string][]string{
	"core":      {},
	"auth":      {"core"},
	"limits":    {"core"},
	"inference": {"auth", "limits", "core"},
	"admin":     {"inference", "auth", "limits", "core"},
	"app":       {"admin", "inference", "auth", "limits", "core"},
}

var vagueNames = []string{"util", "utils", "common", "helper", "helpers", "manager", "managers", "misc", "shared"}

func TestModuleBoundaries(t *testing.T) {
	root := repoRoot(t)
	for file, imports := range goImports(t, root) {
		from := moduleOf(file)
		for _, imp := range imports {
			if !strings.HasPrefix(imp, modulePath+"/") {
				continue
			}
			checkImport(t, file, from, strings.TrimPrefix(imp, modulePath+"/"))
		}
	}
}

func TestModulesAreDeclared(t *testing.T) {
	root := repoRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() {
			continue
		}
		if _, ok := allowedDeps[name]; !ok {
			t.Errorf("internal/%s 는 설계에 없는 모듈이다. 모듈을 늘리려면 설계 문서와 allowedDeps 를 먼저 고친다", name)
		}
	}
}

func checkImport(t *testing.T, file, from, target string) {
	t.Helper()
	parts := strings.Split(target, "/")
	for _, segment := range parts {
		if slices.Contains(vagueNames, segment) {
			t.Errorf("%s: 뭉뚱그린 패키지 이름 %q (기능별로 나눈다)", file, target)
		}
	}
	switch {
	case from == "cmd":
		if target != "internal/app" {
			t.Errorf("%s: cmd 는 internal/app 만 가져온다 (가져온 것: %s)", file, target)
		}
	case parts[0] == "internal" && len(parts) > 1:
		to := parts[1]
		if to != from && !slices.Contains(allowedDeps[from], to) {
			t.Errorf("%s: %s → %s 의존 금지 (설계 3장. %s 가 가져올 수 있는 모듈: %v)", file, from, to, from, allowedDeps[from])
		}
	}
}

// moduleOf 는 파일이 속한 모듈 이름이다(internal/<모듈>/… 의 <모듈>, cmd/… 는 "cmd")
func moduleOf(file string) string {
	parts := strings.Split(filepath.ToSlash(file), "/")
	if parts[0] == "cmd" {
		return "cmd"
	}
	if parts[0] == "internal" && len(parts) > 2 {
		return parts[1]
	}
	return ""
}

func goImports(t *testing.T, root string) map[string][]string {
	t.Helper()
	result := map[string][]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, spec := range parsed.Imports {
			value, _ := strconv.Unquote(spec.Path.Value)
			result[rel] = append(result[rel], value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}
