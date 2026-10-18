package daemon

import "os"

func exportParentOwned(_ os.FileInfo) bool { return false }
