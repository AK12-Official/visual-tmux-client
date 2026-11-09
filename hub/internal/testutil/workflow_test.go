package testutil_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflowStep struct {
	Run             string `yaml:"run"`
	ContinueOnError bool   `yaml:"continue-on-error"`
}

type workflowJob struct {
	Needs           []string          `yaml:"needs"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Permissions     map[string]string `yaml:"permissions"`
	Steps           []workflowStep    `yaml:"steps"`
}

// A scalar or list is legal in Actions; our pipeline consistently uses lists.
func TestReleaseCannotBypassValidation(t *testing.T) {
	path := filepath.Join("..", "..", "..", ".github", "workflows", "ci.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		On          map[string]yaml.Node   `yaml:"on"`
		Permissions map[string]string      `yaml:"permissions"`
		Jobs        map[string]workflowJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"push", "pull_request"} {
		if _, ok := workflow.On[event]; !ok {
			t.Errorf("missing %s trigger", event)
		}
	}
	for name, want := range map[string][]string{
		"test": nil, "package": {"test"}, "assemble": {"test", "package"}, "publish": {"test", "assemble"},
	} {
		job, ok := workflow.Jobs[name]
		if !ok {
			t.Fatalf("missing %s job", name)
		}
		if !reflect.DeepEqual(job.Needs, want) {
			t.Errorf("%s needs %v, want %v", name, job.Needs, want)
		}
		if job.ContinueOnError {
			t.Errorf("%s must fail closed", name)
		}
		if name != "publish" && job.If != "" {
			t.Errorf("%s must use the default success gate", name)
		}
		for _, step := range job.Steps {
			if step.ContinueOnError {
				t.Errorf("%s step must not ignore failure", name)
			}
		}
	}
	wantGate := "success() && github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')"
	if got := workflow.Jobs["publish"].If; got != wantGate {
		t.Errorf("unsafe publish gate: %q", got)
	}
	if workflow.Permissions["contents"] != "read" {
		t.Error("default token must be read-only")
	}
	for name, job := range workflow.Jobs {
		if name != "publish" && job.Permissions["contents"] == "write" {
			t.Errorf("%s must not publish", name)
		}
	}
	if workflow.Jobs["publish"].Permissions["contents"] != "write" {
		t.Error("publisher needs contents:write")
	}
	var checks strings.Builder
	for _, step := range workflow.Jobs["test"].Steps {
		checks.WriteString(step.Run + "\n")
	}
	for _, required := range []string{
		"npm ci && npm run build", "npm test", "golangci-lint run", "go test ./...", "go vet ./...",
	} {
		if !strings.Contains(checks.String(), required) {
			t.Errorf("validation is missing %q", required)
		}
	}
}

func TestNoIndependentReleaseWorkflow(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", ".github", "workflows", "*.y*ml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if filepath.Base(path) == "ci.yml" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "gh release") || strings.Contains(string(data), "contents: write") {
			t.Errorf("%s introduces an independent publishing path; integrate it into the validated CI DAG", path)
		}
	}
}
