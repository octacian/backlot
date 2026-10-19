package daemon

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

const embeddedLogBytes = 256 << 10

type exportLogPreview struct {
	Available    bool
	Head         string
	Tail         string
	TotalRecords int
	ShownRecords int
	TotalBytes   int64
	OmittedBytes int64
}

type exportHTMLView struct {
	v1.ExportReport
	Logs exportLogPreview
}

// readExportLogPreview keeps complete formatted records within the approved
// 256 KiB text budget. Large logs retain head and tail, with explicit omissions.
// Binary payloads retain their original base64 representation and are labelled.
func readExportLogPreview(root *os.Root) (exportLogPreview, error) {
	var preview exportLogPreview
	file, err := root.Open("logs.ndjson")
	if os.IsNotExist(err) {
		return preview, nil
	}
	if err != nil {
		return preview, err
	}
	defer func() { _ = file.Close() }()
	preview.Available = true
	decoder := json.NewDecoder(file)
	var small, head, tail []string
	smallBytes, headBytes, tailBytes := 0, 0, 0
	truncated := false
	appendTail := func(text string) {
		if len(text) > embeddedLogBytes/2 {
			tail = nil
			tailBytes = 0
			return
		}
		tail = append(tail, text)
		tailBytes += len(text)
		for tailBytes > embeddedLogBytes/2 {
			tailBytes -= len(tail[0])
			tail = tail[1:]
		}
	}
	for {
		var record v1.LogRecord
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return preview, err
		}
		payload := record.Message
		if record.Data != "" {
			decoded, err := base64.StdEncoding.DecodeString(record.Data)
			if err != nil {
				return preview, err
			}
			payload = fmt.Sprintf("[binary chunk: %d original bytes; base64 follows]\n%s", len(decoded), record.Data)
		}
		text := fmt.Sprintf("%s · %s · %s\n%s\n", record.Time, record.Component, record.Stream, payload)
		preview.TotalRecords++
		preview.TotalBytes += int64(len(text))
		if !truncated {
			small = append(small, text)
			smallBytes += len(text)
			if smallBytes <= embeddedLogBytes {
				continue
			}
			truncated = true
			for _, item := range small {
				if headBytes+len(item) <= embeddedLogBytes/2 {
					head = append(head, item)
					headBytes += len(item)
				} else {
					break
				}
			}
			for _, item := range small {
				appendTail(item)
			}
			small = nil
		} else {
			appendTail(text)
		}
	}
	if !truncated {
		preview.Head = strings.Join(small, "")
		preview.ShownRecords = preview.TotalRecords
		return preview, nil
	}
	preview.Head = strings.Join(head, "")
	preview.Tail = strings.Join(tail, "")
	preview.ShownRecords = len(head) + len(tail)
	preview.OmittedBytes = preview.TotalBytes - int64(headBytes+tailBytes)
	return preview, nil
}
