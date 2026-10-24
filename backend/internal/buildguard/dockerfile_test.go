package buildguard

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var forbiddenInRuntime = []string{"custody-keygen", "gasstation-keygen", "custody-reencrypt", "build-tools"}

// stages splits the Dockerfile into stage name -> its lines, and returns the
// stage order.
func stages(t *testing.T) (map[string][]string, []string) {
	t.Helper()
	raw, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatalf("no se pudo leer el Dockerfile: %v", err)
	}
	fromRe := regexp.MustCompile(`(?i)^\s*FROM\s+\S+\s+AS\s+(\S+)`)
	out := map[string][]string{}
	var order []string
	cur := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if m := fromRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
			order = append(order, cur)
		}
		if cur != "" {
			out[cur] = append(out[cur], line)
		}
	}
	return out, order
}

func TestRuntimeIsLastStageAndHasNoKeyTools(t *testing.T) {
	st, order := stages(t)
	if len(order) == 0 || order[len(order)-1] != "runtime" {
		t.Fatalf("la última etapa debe ser 'runtime' (build por defecto = producción); orden: %v", order)
	}
	for _, line := range st["runtime"] {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		for _, bad := range forbiddenInRuntime {
			if strings.Contains(trim, bad) {
				t.Errorf("la imagen de producción referencia %q: %s", bad, trim)
			}
		}
	}
}

func TestKeyToolsAreNotBuiltInSharedBuildStage(t *testing.T) {
	st, _ := stages(t)
	for _, line := range st["build"] {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			continue
		}
		for _, bad := range []string{"custody-keygen", "gasstation-keygen", "custody-reencrypt"} {
			if strings.Contains(trim, bad) {
				t.Errorf("la etapa 'build' (compartida con runtime) compila %q: %s", bad, trim)
			}
		}
	}
}

func TestToolsStageShipsTheThreeTools(t *testing.T) {
	st, _ := stages(t)
	joined := strings.Join(st["tools"], "\n")
	for _, want := range []string{"custody-keygen", "gasstation-keygen", "custody-reencrypt"} {
		if !strings.Contains(joined, want) {
			t.Errorf("la etapa 'tools' no incluye %s", want)
		}
	}
}
