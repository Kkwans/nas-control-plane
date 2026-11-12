package docker

import "testing"

func TestImagePullResultBinding(t *testing.T) {
	for _, tc := range []struct {
		name, request, receipt string
		completed, valid       bool
	}{
		{"tag", "alpine:latest", "alpine:latest", true, true},
		{"trim request", " \talpine:latest\n", "alpine:latest", true, true},
		{"digest", "repo@sha256:abc", "repo@sha256:abc", true, true},
		{"existing loose syntax", "repo:", "repo:", true, true},
		{"wrong image", "alpine:latest", "nginx:latest", true, false},
		{"wrong tag", "alpine:latest", "alpine:other", true, false},
		{"registry alias", "alpine:latest", "docker.io/library/alpine:latest", true, false},
		{"case", "alpine:latest", "Alpine:latest", true, false},
		{"digest mismatch", "repo@sha256:abc", "repo@sha256:def", true, false},
		{"blank", "alpine:latest", " \t", true, false},
		{"untrimmed receipt", "alpine:latest", " alpine:latest ", true, false},
		{"incomplete", "alpine:latest", "alpine:latest", false, false},
		{"invalid request", "alpine", "alpine", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := ImagePullRequest{Reference: tc.request, ExpectedBytes: 123}
			result := ImagePullResult{Reference: tc.receipt, Completed: tc.completed}
			beforeRequest, beforeResult := request, result
			if err := ValidateImagePullResult(request, result); (err == nil) != tc.valid {
				t.Fatalf("error=%v valid=%v", err, tc.valid)
			}
			if request != beforeRequest || result != beforeResult {
				t.Fatal("input mutated")
			}
		})
	}
}
