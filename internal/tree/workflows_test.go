package tree

import (
	"testing"
)

// Issue #53: fanpyp's two workflows run only on a push to main, and every
// merge waited the whole checks window for a check that could not come.
func TestWorkflowCanRunOnPR(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		// what can make a check on the PR's head
		{"pull_request scalar", "on: pull_request\njobs: {}\n", true},
		{"flow list", "on: [push, pull_request]\n", true},
		{"block list", "on:\n  - workflow_dispatch\n  - pull_request\n", true},
		{"quoted key", "\"on\":\n  pull_request:\n", true},
		{"yaml 1.1 true key", "true:\n  pull_request:\n", true},
		{"pull_request_target", "on:\n  pull_request_target:\n    types: [opened]\n", true},
		{"pull_request into main", "on:\n  pull_request:\n    branches: [main]\n", true},
		{"pull_request into main, block list", "on:\n  pull_request:\n    branches:\n      - 'main'\n", true},
		{"pull_request into a glob", "on:\n  pull_request:\n    branches: ['ma*']\n", true},
		{"push without a filter", "on: push\n", true},
		{"push to every branch", "on:\n  push:\n    branches: ['**']\n", true},
		{"push to feat/*", "on:\n  push:\n    branches: [feat/*]\n", true},
		{"push with branches-ignore", "on:\n  push:\n    branches-ignore: [main]\n", true},
		{"push with a negated pattern", "on:\n  push:\n    branches: ['!main']\n", true},
		{"push with a character class", "on:\n  push:\n    branches: ['feat/[a-z]*']\n", true},
		{"create", "on: create\n", true},
		{"an event trellis does not know", "on: something_new\n", true},
		{"pull_request with paths", "on:\n  pull_request:\n    paths: ['docs/**']\n", true},
		{"pull_request labeled", "on:\n  pull_request:\n    types: [labeled, closed]\n", true},
		// what the wall cannot read keeps the wait
		{"no on key", "name: x\njobs: {}\n", true},
		{"flow mapping", "on: {push: {branches: [main]}}\n", true},
		{"anchor", "on: &events\n  push:\n    branches: [main]\n", true},
		{"alias", "on: *events\n", true},
		{"merge key", "on:\n  <<: *base\n", true},
		// what cannot
		{"push to main", "on:\n  push:\n    branches: [main]\n", false},
		{"push to main, comments and quotes", "# deploy\non: # when\n  push:   # pushes\n    branches: [ \"main\" ]  # only main\n", false},
		{"push to main, block list", "on:\n  push:\n    branches:\n      - main\n      - release/*\n", false},
		{"push of tags only", "on:\n  push:\n    tags: ['v*']\n", false},
		{"pull_request into release", "on:\n  pull_request:\n    branches: [release]\n", false},
		{"pull_request closed", "on:\n  pull_request:\n    types: [closed]\n", false},
		{"schedule and dispatch", "on:\n  schedule:\n    - cron: '0 3 * * *'\n  workflow_dispatch:\n", false},
		{"merge_group and release", "on: [merge_group, release]\n", false},
		{"push to main among others", "name: deploy\non:\n  push:\n    branches: [main]\n  workflow_dispatch:\n    inputs:\n      env:\n        type: string\njobs:\n  d:\n    runs-on: ubuntu-latest\n", false},
		{"crlf", "on:\r\n  push:\r\n    branches: [main]\r\n", false},
	} {
		if got := workflowCanRunOnPR([]byte(tc.src), "feat/x", "main"); got != tc.want {
			t.Errorf("%s: workflowCanRunOnPR = %v, want %v\n%s", tc.name, got, tc.want, tc.src)
		}
	}
}
