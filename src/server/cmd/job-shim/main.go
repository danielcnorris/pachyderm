package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/pachyderm/pachyderm/src/client"
	ppsclient "github.com/pachyderm/pachyderm/src/client/pps"
	"github.com/pachyderm/pachyderm/src/server/pfs/fuse"
	ppsserver "github.com/pachyderm/pachyderm/src/server/pps"
	"github.com/spf13/cobra"
	"go.pedge.io/env"
	"go.pedge.io/lion"
	"golang.org/x/net/context"
)

type appEnv struct {
	PachydermAddress string `env:"PACHD_PORT_650_TCP_ADDR,required"`
}

func main() {
	env.Main(do, &appEnv{})
}

func do(appEnvObj interface{}) error {
	appEnv := appEnvObj.(*appEnv)
	rootCmd := &cobra.Command{
		Use:   os.Args[0] + " job-id",
		Short: `Pachyderm job-shim, coordinates with ppsd to create an output commit and run user work.`,
		Long:  `Pachyderm job-shim, coordinates with ppsd to create an output commit and run user work.`,
		Run: func(_ *cobra.Command, args []string) {
			ppsClient, err := ppsserver.NewInternalJobAPIClientFromAddress(fmt.Sprintf("%v:650", appEnv.PachydermAddress))
			if err != nil {
				errorAndExit(err.Error())
			}
			response, err := ppsClient.StartJob(
				context.Background(),
				&ppsserver.StartJobRequest{
					Job: &ppsclient.Job{
						ID: args[0],
					}})
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s\n", err.Error())
				os.Exit(0)
			}

			if response.Transform.Debug {
				lion.SetLevel(lion.LevelDebug)
			}

			// We want to make sure that we only send FinishJob once.
			// The most bulletproof way would be to check that on server side,
			// but this is easier.
			var finished bool
			// Make sure that we call FinishJob even if something caused a panic
			defer func() {
				if r := recover(); r != nil && !finished {
					fmt.Println("job shim crashed; this is like a bug in pachyderm")
					if _, err := ppsClient.FinishJob(
						context.Background(),
						&ppsserver.FinishJobRequest{
							Job: &ppsclient.Job{
								ID: args[0],
							},
							Success: false,
						},
					); err != nil {
						errorAndExit(err.Error())
					}
				}
			}()

			c, err := client.NewFromAddress(fmt.Sprintf("%v:650", appEnv.PachydermAddress))
			if err != nil {
				errorAndExit(err.Error())
			}

			mounter := fuse.NewMounter(appEnv.PachydermAddress, c.PfsAPIClient)
			ready := make(chan bool)
			go func() {
				if err := mounter.MountAndCreate(
					"/pfs",
					nil,
					response.CommitMounts,
					ready,
					response.Transform.Debug,
				); err != nil {
					errorAndExit(err.Error())
				}
			}()
			<-ready
			defer func() {
				if err := mounter.Unmount("/pfs"); err != nil {
					errorAndExit(err.Error())
				}
			}()
			var readers []io.Reader
			for _, line := range response.Transform.Stdin {
				readers = append(readers, strings.NewReader(line+"\n"))
			}
			if len(response.Transform.Cmd) == 0 {
				fmt.Println("unable to run; a cmd needs to be provided")
				if _, err := ppsClient.FinishJob(
					context.Background(),
					&ppsserver.FinishJobRequest{
						Job:     client.NewJob(args[0]),
						Success: false,
					},
				); err != nil {
					errorAndExit(err.Error())
				}
				finished = true
				return
			}
			cmd := exec.Command(response.Transform.Cmd[0], response.Transform.Cmd[1:]...)
			cmd.Stdin = io.MultiReader(readers...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			success := true
			if err := cmd.Run(); err != nil {
				success = false
				if exiterr, ok := err.(*exec.ExitError); ok {
					if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
						for _, returnCode := range response.Transform.AcceptReturnCode {
							if int(returnCode) == status.ExitStatus() {
								success = true
							}
						}
					}
				}
				if !success {
					fmt.Fprintf(os.Stderr, "Error from exec: %s\n", err.Error())
				}
			}
			if _, err := ppsClient.FinishJob(
				context.Background(),
				&ppsserver.FinishJobRequest{
					Job:     client.NewJob(args[0]),
					Success: success,
				},
			); err != nil {
				errorAndExit(err.Error())
			}
			finished = true
		},
	}

	return rootCmd.Execute()
}

func errorAndExit(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}
