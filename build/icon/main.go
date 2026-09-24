package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fyne-io/oksvg"
	"github.com/srwiley/rasterx"
)

func main() {
	source := flag.String("source", "images/myapp-app.svg", "SVG 文件")
	output := flag.String("output", "images/generated/myapp.png", "PNG 文件")
	size := flag.Int("size", 512, "PNG 边长")
	ico := flag.String("ico", "", "输出 ICO 文件")
	icoInputs := flag.String("ico-inputs", "", "ICO 输入 PNG，使用逗号分隔")
	icns := flag.String("icns", "", "输出 ICNS 文件")
	icnsInputs := flag.String("icns-inputs", "", "ICNS 输入 PNG，使用逗号分隔")
	flag.Parse()
	if *ico != "" {
		must(writeICO(*ico, strings.Split(*icoInputs, ",")))
		return
	}
	if *icns != "" {
		must(writeICNS(*icns, strings.Split(*icnsInputs, ",")))
		return
	}

	input, err := os.Open(*source)
	must(err)
	defer input.Close()
	icon, err := oksvg.ReadIconStream(input)
	must(err)
	icon.SetTarget(0, 0, float64(*size), float64(*size))
	imageValue := image.NewRGBA(image.Rect(0, 0, *size, *size))
	scanner := rasterx.NewScannerGV(*size, *size, imageValue, imageValue.Bounds())
	icon.Draw(rasterx.NewDasher(*size, *size, scanner), 1)
	must(os.MkdirAll(filepath.Dir(*output), 0o755))
	file, err := os.Create(*output)
	must(err)
	must(png.Encode(file, imageValue))
	must(file.Close())
}

func writeICO(output string, inputs []string) error {
	if len(inputs) == 0 {
		return os.ErrInvalid
	}
	data := make([][]byte, len(inputs))
	for index, input := range inputs {
		value, err := os.ReadFile(input)
		if err != nil {
			return err
		}
		data[index] = value
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := binary.Write(file, binary.LittleEndian, uint16(0)); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(1)); err != nil {
		return err
	}
	if err := binary.Write(file, binary.LittleEndian, uint16(len(data))); err != nil {
		return err
	}
	offset := uint32(6 + 16*len(data))
	for index, value := range data {
		size := strings.TrimSuffix(filepath.Base(inputs[index]), filepath.Ext(inputs[index]))
		size = strings.TrimPrefix(size, "app-")
		parsed, parseErr := strconv.Atoi(size)
		if parseErr != nil {
			return parseErr
		}
		width := byte(parsed)
		if parsed >= 256 {
			width = 0
		}
		entry := []byte{width, width, 0, 0, 1, 0, 32, 0}
		entry = append(entry, make([]byte, 8)...)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(value)))
		binary.LittleEndian.PutUint32(entry[12:16], offset)
		if _, err := file.Write(entry); err != nil {
			return err
		}
		offset += uint32(len(value))
	}
	for _, value := range data {
		if _, err := io.Copy(file, bytes.NewReader(value)); err != nil {
			return err
		}
	}
	return nil
}

func writeICNS(output string, inputs []string) error {
	types := map[string]string{"16": "icp4", "32": "icp5", "64": "icp6", "128": "ic07", "256": "ic08", "512": "ic09", "1024": "ic10"}
	type chunk struct {
		kind string
		data []byte
	}
	var chunks []chunk
	for _, input := range inputs {
		value, err := os.ReadFile(input)
		if err != nil {
			return err
		}
		base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		size := strings.TrimPrefix(base, "app-")
		kind, ok := types[size]
		if !ok {
			return os.ErrInvalid
		}
		chunks = append(chunks, chunk{kind: kind, data: value})
	}
	length := uint32(8)
	for _, part := range chunks {
		length += uint32(8 + len(part.data))
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write([]byte("icns")); err != nil {
		return err
	}
	if err := binary.Write(file, binary.BigEndian, length); err != nil {
		return err
	}
	for _, part := range chunks {
		if _, err := file.Write([]byte(part.kind)); err != nil {
			return err
		}
		if err := binary.Write(file, binary.BigEndian, uint32(8+len(part.data))); err != nil {
			return err
		}
		if _, err := file.Write(part.data); err != nil {
			return err
		}
	}
	return nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
