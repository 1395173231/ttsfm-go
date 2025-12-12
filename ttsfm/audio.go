package ttsfm

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

// CombineAudioChunks 合并多个音频块
func CombineAudioChunks(chunks [][]byte, format string) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no audio chunks to combine")
	}

	if len(chunks) == 1 {
		return chunks[0], nil
	}

	switch format {
	case "mp3":
		return combineMP3Chunks(chunks)
	case "wav":
		return combineWAVChunks(chunks)
	default:
		return combineRawChunks(chunks), nil
	}
}

// combineMP3Chunks 合并 MP3 音频块
// MP3 文件可以简单拼接（除去后续块可能包含的 ID3 标签）
func combineMP3Chunks(chunks [][]byte) ([]byte, error) {
	var result bytes.Buffer

	for i, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}

		data := chunk
		if i > 0 {
			data = skipID3Tag(data)
		}

		_, _ = result.Write(data)
	}

	return result.Bytes(), nil
}

// skipID3Tag 跳过 ID3v2 标签（若存在）
func skipID3Tag(data []byte) []byte {
	if len(data) < 10 {
		return data
	}

	if data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		size := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
		totalSize := size + 10

		if data[5]&0x10 != 0 {
			totalSize += 10
		}

		if totalSize < len(data) {
			return data[totalSize:]
		}
	}

	return data
}

// combineWAVChunks 合并 WAV 音频块
func combineWAVChunks(chunks [][]byte) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks to combine")
	}

	firstHeader, err := parseWAVHeader(chunks[0])
	if err != nil {
		return combineRawChunks(chunks), nil
	}

	var audioData bytes.Buffer
	for _, chunk := range chunks {
		data, err := extractWAVData(chunk)
		if err != nil {
			_, _ = audioData.Write(chunk)
		} else {
			_, _ = audioData.Write(data)
		}
	}

	return createWAVFile(firstHeader, audioData.Bytes())
}

// WAVHeader WAV 文件头信息
type WAVHeader struct {
	AudioFormat   uint16
	NumChannels   uint16
	SampleRate    uint32
	ByteRate      uint32
	BlockAlign    uint16
	BitsPerSample uint16
}

// parseWAVHeader 解析 WAV 文件头
func parseWAVHeader(data []byte) (*WAVHeader, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("data too short for WAV header")
	}

	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a valid WAV file")
	}

	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "fmt " {
			if offset+8+int(chunkSize) > len(data) {
				return nil, fmt.Errorf("fmt chunk extends beyond file")
			}

			fmtData := data[offset+8 : offset+8+int(chunkSize)]
			if len(fmtData) < 16 {
				return nil, fmt.Errorf("fmt chunk too small")
			}

			return &WAVHeader{
				AudioFormat:   binary.LittleEndian.Uint16(fmtData[0:2]),
				NumChannels:   binary.LittleEndian.Uint16(fmtData[2:4]),
				SampleRate:    binary.LittleEndian.Uint32(fmtData[4:8]),
				ByteRate:      binary.LittleEndian.Uint32(fmtData[8:12]),
				BlockAlign:    binary.LittleEndian.Uint16(fmtData[12:14]),
				BitsPerSample: binary.LittleEndian.Uint16(fmtData[14:16]),
			}, nil
		}

		offset += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil, fmt.Errorf("fmt chunk not found")
}

// extractWAVData 提取 WAV 音频数据
func extractWAVData(data []byte) ([]byte, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("data too short")
	}

	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "data" {
			dataStart := offset + 8
			dataEnd := dataStart + int(chunkSize)
			if dataEnd > len(data) {
				dataEnd = len(data)
			}
			return data[dataStart:dataEnd], nil
		}

		offset += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil, fmt.Errorf("data chunk not found")
}

// createWAVFile 创建 WAV 文件
func createWAVFile(header *WAVHeader, audioData []byte) ([]byte, error) {
	var buf bytes.Buffer

	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, fileSize)
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, header.AudioFormat)
	_ = binary.Write(&buf, binary.LittleEndian, header.NumChannels)
	_ = binary.Write(&buf, binary.LittleEndian, header.SampleRate)
	_ = binary.Write(&buf, binary.LittleEndian, header.ByteRate)
	_ = binary.Write(&buf, binary.LittleEndian, header.BlockAlign)
	_ = binary.Write(&buf, binary.LittleEndian, header.BitsPerSample)

	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, dataSize)
	_, _ = buf.Write(audioData)

	return buf.Bytes(), nil
}

// combineRawChunks 简单拼接音频块
func combineRawChunks(chunks [][]byte) []byte {
	var result bytes.Buffer
	for _, chunk := range chunks {
		_, _ = result.Write(chunk)
	}
	return result.Bytes()
}

// AudioCombiner 音频合并器接口
type AudioCombiner interface {
	Combine(chunks [][]byte) ([]byte, error)
}

// StreamingAudioCombiner 流式音频合并器
type StreamingAudioCombiner struct {
	format string
	buffer bytes.Buffer
	count  int
}

// NewStreamingAudioCombiner 创建流式合并器
func NewStreamingAudioCombiner(format string) *StreamingAudioCombiner {
	return &StreamingAudioCombiner{
		format: format,
	}
}

// Add 添加音频块
func (c *StreamingAudioCombiner) Add(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}

	if c.format == "mp3" && c.count > 0 {
		chunk = skipID3Tag(chunk)
	}

	_, _ = c.buffer.Write(chunk)
	c.count++
	return nil
}

// Result 获取合并结果
func (c *StreamingAudioCombiner) Result() []byte {
	return c.buffer.Bytes()
}

// Count 获取块数量
func (c *StreamingAudioCombiner) Count() int {
	return c.count
}

// WriteTo 写入到 Writer
func (c *StreamingAudioCombiner) WriteTo(w io.Writer) (int64, error) {
	n, err := w.Write(c.buffer.Bytes())
	return int64(n), err
}