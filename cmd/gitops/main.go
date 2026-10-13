// gitops is built and executed by GitLab from the reviewed management project.
// It does not load the administrator's bootstrap config or local state.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/gitops"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: gitops validate|plan|apply|host-runtime [project]")
		os.Exit(2)
	}
	project := "."
	if len(os.Args) > 2 {
		project = os.Args[2]
	}
	env := map[string]string{}
	for _, value := range os.Environ() {
		key, item, _ := strings.Cut(value, "=")
		env[key] = item
	}
	runner := &gitops.Runner{Project: project, Env: env, Output: os.Stdout}
	var err error
	if os.Args[1] == "validate-manifests" {
		var data []byte
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
		if err == nil {
			err = gitops.ValidateManifests(data, project)
		}
		if err == nil {
			_, err = os.Stdout.Write(data)
		}
	} else if os.Args[1] == "host-runtime" {
		err = runner.HostRuntime(context.Background(), os.Stdin)
	} else {
		err = runner.Run(context.Background(), os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "GitOps:", err)
		os.Exit(1)
	}
}
