package localipc

import "os"

func owned(_ os.FileInfo) bool { return false }
