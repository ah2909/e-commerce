package api_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFrameworkImportsStayOutOfApplicationAndDomain(t *testing.T) {
	root := filepath.Join("..", "internal")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return walkErr
		}
		normalized := filepath.ToSlash(path)
		if !strings.Contains(normalized, "/application/") && !strings.Contains(normalized, "/domain/") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			name, _ := strconv.Unquote(spec.Path.Value)
			if strings.HasPrefix(name, "github.com/gin-gonic/gin") || strings.HasPrefix(name, "gorm.io/") {
				t.Errorf("%s imports adapter framework %s", normalized, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDoesNotMutateSchemaThroughGORM(t *testing.T) {
	forbidden := map[string]struct{}{"Auto" + "Migrate": {}, "Migrator": {}}
	err := filepath.WalkDir(filepath.Join("..", "internal"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok {
				if _, blocked := forbidden[identifier.Name]; blocked {
					t.Errorf("%s contains forbidden GORM schema operation %s", filepath.ToSlash(path), identifier.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
