package interfaces

import (
	"io"

	S "github.com/compozed/deployadactyl/structs"
)

// Pusher interface.
type Pusher interface {
	Login(foundationURL string, deploymentInfo S.DeploymentInfo, response io.Writer) error
	Push(appPath string, deploymentInfo S.DeploymentInfo, response io.Writer) error
	UndoPush(deploymentInfo S.DeploymentInfo) error
	FinishPush(deploymentInfo S.DeploymentInfo) error
	CleanUp() error
	Exists(appName string)
}
