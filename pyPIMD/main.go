package main

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	bufra "github.com/avvmoto/buf-readerat"
	"github.com/snabb/httpreaderat"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func run() error {
	req, _ := http.NewRequest("GET", os.Args[1], nil)

	htrdr, err := httpreaderat.New(nil, req, nil)
	if err != nil {
		panic(err)
	}
	bhtrdr := bufra.NewBufReaderAt(htrdr, 1024*1024)

	z, err := zip.NewReader(bhtrdr, htrdr.Size())

	for _, f := range z.File {
		if strings.HasSuffix(f.Name, "info/METADATA") || strings.HasSuffix(f.Name, "EGG-INFO/PKG-INFO") {
			r, err := f.Open()
			if err != nil {
				return err
			}

			_, err = io.Copy(os.Stdout, r)
			if err != nil {
				return err
			}

			break
		}
	}

	return nil
}
