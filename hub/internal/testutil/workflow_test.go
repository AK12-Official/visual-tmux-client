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
	Run             string            `yaml:"run"`
	Uses            string            `yaml:"uses"`
	If              string            `yaml:"if"`
	With            map[string]string `yaml:"with"`
	ContinueOnError bool              `yaml:"continue-on-error"`
}

type workflowJob struct {
	Needs           []string          `yaml:"needs"`
	If              string            `yaml:"if"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Permissions     map[string]string `yaml:"permissions"`
	Steps           []workflowStep    `yaml:"steps"`
	Outputs         map[string]string `yaml:"outputs"`
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
		Concurrency struct {
			Group  string `yaml:"group"`
			Cancel string `yaml:"cancel-in-progress"`
		} `yaml:"concurrency"`
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
		"changes": nil, "validate": {"changes"}, "package": {"changes", "validate"},
		"assemble": {"changes", "validate", "package"}, "test": {"changes", "validate", "package", "assemble"},
		"publish": {"changes", "test", "assemble"},
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
		wantCondition := map[string]string{
			"validate": "needs.changes.outputs.full == 'true'", "test": "always()",
			"publish": "success() && github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')",
		}[name]
		if job.If != wantCondition {
			t.Errorf("%s has unsafe condition %q", name, job.If)
		}
		for _, step := range job.Steps {
			if step.ContinueOnError {
				t.Errorf("%s step must not ignore failure", name)
			}
		}
	}
	// Keep the entire workflow observable; do not leave required checks Pending.
	for _, event := range []string{"push", "pull_request"} {
		var filters map[string]yaml.Node
		node := workflow.On[event]
		if err := node.Decode(&filters); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"paths", "paths-ignore"} {
			if _, ok := filters[key]; ok {
				t.Errorf("%s must not skip the workflow via %s", event, key)
			}
		}
	}
	if workflow.Jobs["changes"].Permissions["pull-requests"] != "read" {
		t.Error("PR classification needs read permission")
	}
	fullGate := "${{ startsWith(github.ref, 'refs/tags/v') || steps.paths.outputs.full != 'false' }}"
	if workflow.Jobs["changes"].Outputs["full"] != fullGate {
		t.Error("tags and unknown classification must run full CI")
	}
	foundPaths, foundStatus := false, false
	for _, step := range workflow.Jobs["changes"].Steps {
		if !strings.HasPrefix(step.Uses, "dorny/paths-filter@") {
			continue
		}
		foundPaths = true
		if step.Uses != "dorny/paths-filter@7b450fff21473bca461d4b92ce414b9d0420d706" {
			t.Error("pin the audited filter action")
		}
		if step.With["predicate-quantifier"] != "every" {
			t.Error("exclusions require every-pattern matching")
		}
		var paths map[string][]string
		if err := yaml.Unmarshal([]byte(step.With["filters"]), &paths); err != nil {
			t.Fatal(err)
		}
		want := []string{"**", "!docs/**", "!openspec/**", "!*.md"}
		if !reflect.DeepEqual(paths["full"], want) {
			t.Errorf("unsafe docs exclusion: %v", paths["full"])
		}
	}
	for _, step := range workflow.Jobs["test"].Steps {
		if step.Run == "node scripts/ci-status.mjs" {
			foundStatus = true
		}
	}
	if !foundPaths || !foundStatus {
		t.Error("classification and explicit aggregate result validation are mandatory")
	}
	if workflow.Concurrency.Group != "${{ github.workflow }}-${{ github.event.pull_request.number || github.run_id }}" {
		t.Error("only PRs should share a workflow concurrency group")
	}
	if workflow.Concurrency.Cancel != "${{ github.event_name == 'pull_request' }}" {
		t.Error("never auto-cancel main or release runs")
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
	for _, step := range workflow.Jobs["validate"].Steps {
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
