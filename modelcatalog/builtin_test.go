package modelcatalog_test

import (
	"sync"
	"testing"

	"github.com/sylumi/agentkit/modelcatalog"
)

func TestBuiltinConcurrentReuse(t *testing.T) {
	const callers = 12
	start := make(chan struct{})
	results := make(chan *modelcatalog.Catalog, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			<-start
			catalog, err := modelcatalog.Builtin()
			if err != nil {
				t.Error(err)
				return
			}
			results <- catalog
		})
	}
	close(start)
	wg.Wait()
	close(results)

	var first *modelcatalog.Catalog
	for catalog := range results {
		if first == nil {
			first = catalog
		} else if catalog != first {
			t.Fatal("concurrent callers received different built-in catalogs")
		}
	}
}

func TestDefaultQueries(t *testing.T) {
	info, err := modelcatalog.Lookup("deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	if info.Provider.ID != "deepseek" || info.ID != "deepseek-flash" || info.Limits.ContextTokens == nil {
		t.Fatal("missing built-in model details")
	}
	wantLimit := *info.Limits.ContextTokens
	*info.Limits.ContextTokens = -1

	models, err := modelcatalog.List("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range models {
		if m.Provider.ID != "deepseek" {
			t.Fatal("default list returned a different provider")
		}
		if m.ID == info.ID {
			found = true
			if m.Limits.ContextTokens == nil || *m.Limits.ContextTokens != wantLimit {
				t.Fatal("editing a lookup result changed the default catalog")
			}
			*m.Limits.ContextTokens = -2
		}
	}
	if !found {
		t.Fatal("default list omitted the selected model")
	}
	again, err := modelcatalog.Lookup("deepseek", "deepseek-flash")
	if err != nil {
		t.Fatal(err)
	}
	if again.Limits.ContextTokens == nil || *again.Limits.ContextTokens != wantLimit {
		t.Fatal("editing a list result changed the default catalog")
	}

	for _, key := range [][2]string{{"absent", "deepseek-flash"}, {"deepseek", "absent"}} {
		if _, err := modelcatalog.Lookup(key[0], key[1]); err == nil {
			t.Fatalf("missing model %q/%q did not return an error", key[0], key[1])
		}
	}
	if empty, err := modelcatalog.List("absent"); err != nil || len(empty) != 0 {
		t.Fatalf("missing provider should return an empty list, got error %v", err)
	}
	if all, err := modelcatalog.List(""); err != nil || len(all) <= len(models) {
		t.Fatalf("empty provider ID did not list all providers, got error %v", err)
	}
}

func TestMustLookup(t *testing.T) {
	info := modelcatalog.MustLookup("deepseek", "deepseek-v4-flash")
	if info.Provider.ID != "deepseek" || info.ID != "deepseek-v4-flash" {
		t.Fatalf("unexpected model: %q/%q", info.Provider.ID, info.ID)
	}
	for _, key := range [][2]string{{"absent", "deepseek-v4-flash"}, {"deepseek", "absent"}} {
		t.Run(key[0]+"/"+key[1], func(t *testing.T) {
			_, want := modelcatalog.Lookup(key[0], key[1])
			defer func() {
				got, ok := recover().(error)
				if !ok || want == nil || got.Error() != want.Error() {
					t.Fatalf("panic = %v, want lookup error %v", got, want)
				}
			}()
			modelcatalog.MustLookup(key[0], key[1])
		})
	}
}
