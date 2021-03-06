package blast

import (
	"encoding/csv"
	"os"

	"context"
	"strings"

	"io"

	"cloud.google.com/go/storage"
	"github.com/pkg/errors"
)

func (b *Blaster) openDataFile(ctx context.Context) error {
	var err error
	var rc io.ReadCloser
	if strings.HasPrefix(b.config.Data, "gs://") {
		name := strings.TrimPrefix(b.config.Data, "gs://")
		bucket := name[:strings.Index(name, "/")]
		handle := name[strings.Index(name, "/")+1:]
		client, err := storage.NewClient(ctx)
		if err != nil {
			return errors.WithStack(err)
		}
		rc, err = client.Bucket(bucket).Object(handle).NewReader(ctx)
		if err != nil {
			return errors.WithStack(err)
		}
	} else {
		rc, err = os.Open(b.config.Data)
		if err != nil {
			return errors.WithStack(err)
		}
	}
	b.dataCloser = rc
	b.dataReader = csv.NewReader(rc)
	b.dataHeaders, err = b.dataReader.Read()
	if err != nil {
		return errors.WithStack(err)
	}
	return nil
}

func (b *Blaster) closeDataFile() {
	_ = b.dataCloser.Close() // ignore error
}
