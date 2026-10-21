package daemon

import (
	"github.com/octacian/backlot/internal/localipc"
	"os"
)

func fileIdentity(info os.FileInfo) (string, error) { return localipc.Identity(info) }
