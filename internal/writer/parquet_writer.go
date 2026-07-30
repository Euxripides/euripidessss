package writer

import (
	"errors"
	"io"
	"os"
)

func VerifyParquet(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < 8 {
		return errors.New("Parquet 文件过小")
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(file, header); err != nil {
		return err
	}
	if string(header) != "PAR1" {
		return errors.New("Parquet 文件头校验失败")
	}
	if _, err := file.Seek(-4, io.SeekEnd); err != nil {
		return err
	}
	footer := make([]byte, 4)
	if _, err := io.ReadFull(file, footer); err != nil {
		return err
	}
	if string(footer) != "PAR1" {
		return errors.New("Parquet footer 校验失败")
	}
	return nil
}
