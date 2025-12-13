package ttsfm

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var wavRiffHeader = [12]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'A', 'V', 'E'}

func copyNBuffer(dst io.Writer, src io.Reader, n int64, buf []byte) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	if len(buf) == 0 {
		return 0, fmt.Errorf("buffer size must be > 0")
	}

	var written int64
	for written < n {
		toRead := int64(len(buf))
		remain := n - written
		if remain < toRead {
			toRead = remain
		}

		readN, readErr := src.Read(buf[:toRead])
		if readN > 0 {
			writeN, writeErr := dst.Write(buf[:readN])
			written += int64(writeN)
			if writeErr != nil {
				return written, writeErr
			}
			if writeN != readN {
				return written, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, io.EOF
			}
			return written, readErr
		}
	}

	return written, nil
}

// CopyMP3Stream 将 MP3 数据从 r 写到 w；当 skipID3=true 时会尝试跳过 ID3v2 标签。
// 用于长文本拼接时避免重复写入 ID3 标签。
// 返回写入的字节数（不包含被丢弃的 ID3）。
func CopyMP3Stream(w io.Writer, r io.Reader, skipID3 bool) (int64, error) {
	br := bufio.NewReader(r)

	if skipID3 {
		if err := discardID3v2(br); err != nil {
			return 0, err
		}
	}

	return io.Copy(w, br)
}

// CopyMP3StreamWithBuffer 与 CopyMP3Stream 类似，但允许显式指定拷贝缓冲区大小（buf）。
func CopyMP3StreamWithBuffer(w io.Writer, r io.Reader, skipID3 bool, buf []byte) (int64, error) {
	if len(buf) == 0 {
		return 0, fmt.Errorf("buffer size must be > 0")
	}

	br := bufio.NewReaderSize(r, len(buf))
	if skipID3 {
		if err := discardID3v2(br); err != nil {
			return 0, err
		}
	}
	return io.CopyBuffer(w, br, buf)
}

func discardID3v2(br *bufio.Reader) error {
	header, err := br.Peek(10)
	if err != nil {
		// 数据不足 10 字节时，不做处理
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if len(header) < 10 {
		return nil
	}
	if header[0] != 'I' || header[1] != 'D' || header[2] != '3' {
		return nil
	}

	// ID3v2 size 使用 syncsafe integer（不包含 10 字节 header）
	size := int(header[6])<<21 | int(header[7])<<14 | int(header[8])<<7 | int(header[9])
	total := size + 10
	_, err = br.Discard(total)
	return err
}

// CopyWAVDataStream 解析 WAV 容器并只将 data chunk（PCM 数据）写入 w。
// 适用于流式拼接：第一个 chunk 写完整 WAV（含头），后续 chunk 只写 data，避免重复头。
// 返回写入的 PCM 数据字节数。
func CopyWAVDataStream(w io.Writer, r io.Reader) (int64, error) {
	br := bufio.NewReader(r)

	var header [12]byte
	if _, err := io.ReadFull(br, header[:]); err != nil {
		return 0, err
	}
	if header[0] != wavRiffHeader[0] || header[1] != wavRiffHeader[1] || header[2] != wavRiffHeader[2] ||
		header[3] != wavRiffHeader[3] || header[8] != wavRiffHeader[8] || header[9] != wavRiffHeader[9] ||
		header[10] != wavRiffHeader[10] || header[11] != wavRiffHeader[11] {
		// 不是 WAV，按裸数据写回（把已经读出的 12 字节也写回）
		n1, err := w.Write(header[:])
		if err != nil {
			return int64(n1), err
		}
		n2, err := io.Copy(w, br)
		return int64(n1) + n2, err
	}

	var written int64
	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(br, chunkHeader[:]); err != nil {
			return written, err
		}
		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		if chunkID == "data" {
			n, err := io.CopyN(w, br, int64(chunkSize))
			written += n
			if err != nil && !errors.Is(err, io.EOF) {
				return written, err
			}
			// padding byte（WAV chunk 对齐到 2 字节）
			if chunkSize%2 != 0 {
				_, _ = br.ReadByte()
			}
			return written, nil
		}

		// 丢弃其他 chunk
		if _, err := io.CopyN(io.Discard, br, int64(chunkSize)); err != nil {
			if errors.Is(err, io.EOF) {
				return written, nil
			}
			return written, err
		}
		if chunkSize%2 != 0 {
			_, _ = br.ReadByte()
		}
	}
}

// CopyWAVDataStreamWithBuffer 与 CopyWAVDataStream 类似，但允许显式指定拷贝缓冲区大小（buf）。
func CopyWAVDataStreamWithBuffer(w io.Writer, r io.Reader, buf []byte) (int64, error) {
	if len(buf) == 0 {
		return 0, fmt.Errorf("buffer size must be > 0")
	}

	br := bufio.NewReaderSize(r, len(buf))

	var header [12]byte
	if _, err := io.ReadFull(br, header[:]); err != nil {
		return 0, err
	}
	if header[0] != wavRiffHeader[0] || header[1] != wavRiffHeader[1] || header[2] != wavRiffHeader[2] ||
		header[3] != wavRiffHeader[3] || header[8] != wavRiffHeader[8] || header[9] != wavRiffHeader[9] ||
		header[10] != wavRiffHeader[10] || header[11] != wavRiffHeader[11] {
		// 不是 WAV，按裸数据写回（把已经读出的 12 字节也写回）
		n1, err := w.Write(header[:])
		if err != nil {
			return int64(n1), err
		}
		n2, err := io.CopyBuffer(w, br, buf)
		return int64(n1) + n2, err
	}

	var written int64
	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(br, chunkHeader[:]); err != nil {
			return written, err
		}
		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		if chunkID == "data" {
			n, err := copyNBuffer(w, br, int64(chunkSize), buf)
			written += n
			if err != nil && !errors.Is(err, io.EOF) {
				return written, err
			}
			if chunkSize%2 != 0 {
				_, _ = br.ReadByte()
			}
			return written, nil
		}

		_, err := copyNBuffer(io.Discard, br, int64(chunkSize), buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return written, err
		}
		if chunkSize%2 != 0 {
			_, _ = br.ReadByte()
		}
	}
}

// CombineAudioChunks 合并多个音频块
func CombineAudioChunks(chunks [][]byte, format AudioFormat) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, fmt.Errorf("no audio chunks to combine")
	}
	if len(chunks) == 1 {
		return chunks[0], nil
	}

	switch format {
	case FormatMP3:
		return combineMP3Chunks(chunks)
	case FormatWAV:
		return combineWAVChunks(chunks)
	case FormatOPUS, FormatAAC, FormatFLAC, FormatPCM:
		return combineRawChunks(chunks)
	default:
		return combineRawChunks(chunks)
	}
}

// combineMP3Chunks 合并 MP3 音频块（帧可直接拼接）
func combineMP3Chunks(chunks [][]byte) ([]byte, error) {
	var buffer bytes.Buffer

	for i, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}

		data := chunk
		if i > 0 {
			data = skipID3Tag(data)
		}

		_, _ = buffer.Write(data)
	}

	return buffer.Bytes(), nil
}

// skipID3Tag 跳过 ID3v2 标签（若存在）
func skipID3Tag(data []byte) []byte {
	if len(data) < 10 {
		return data
	}

	if data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		// ID3v2 size 使用 syncsafe integer（不包含 10 字节 header）
		size := int(data[6])<<21 | int(data[7])<<14 | int(data[8])<<7 | int(data[9])
		totalSize := size + 10
		if totalSize < len(data) {
			return data[totalSize:]
		}
	}

	return data
}

// combineWAVChunks 合并 WAV 音频块（需重建 WAV 头并更新数据长度）
func combineWAVChunks(chunks [][]byte) ([]byte, error) {
	firstHeader, err := parseWAVHeader(chunks[0])
	if err != nil {
		// 不是标准 WAV（或返回格式不一致）时退回到原始拼接
		return combineRawChunks(chunks)
	}

	var audioData bytes.Buffer
	for _, chunk := range chunks {
		data, err := extractWAVData(chunk)
		if err != nil {
			// 如果 chunk 看起来像 WAV 但提取失败，直接返回错误避免输出不可播放文件
			if looksLikeWAV(chunk) {
				return nil, fmt.Errorf("failed to extract wav data: %w", err)
			}
			// 否则当作纯音频数据拼接（极少数服务可能返回裸 PCM）
			_, _ = audioData.Write(chunk)
			continue
		}
		_, _ = audioData.Write(data)
	}

	return buildWAVFile(firstHeader, audioData.Bytes())
}

func looksLikeWAV(data []byte) bool {
	return len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WAVE"
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
	if !looksLikeWAV(data) {
		return nil, fmt.Errorf("not a valid WAV file")
	}

	// 查找 fmt chunk
	offset := 12
	for offset < len(data)-8 {
		chunkID := string(data[offset : offset+4])
		chunkSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])

		if chunkID == "fmt " {
			if offset+8+int(chunkSize) > len(data) {
				return nil, fmt.Errorf("fmt chunk extends beyond file")
			}
			if chunkSize < 16 {
				return nil, fmt.Errorf("fmt chunk too small")
			}

			base := offset + 8
			return &WAVHeader{
				AudioFormat:   binary.LittleEndian.Uint16(data[base : base+2]),
				NumChannels:   binary.LittleEndian.Uint16(data[base+2 : base+4]),
				SampleRate:    binary.LittleEndian.Uint32(data[base+4 : base+8]),
				ByteRate:      binary.LittleEndian.Uint32(data[base+8 : base+12]),
				BlockAlign:    binary.LittleEndian.Uint16(data[base+12 : base+14]),
				BitsPerSample: binary.LittleEndian.Uint16(data[base+14 : base+16]),
			}, nil
		}

		offset += 8 + int(chunkSize)
		if chunkSize%2 != 0 {
			offset++
		}
	}

	return nil, fmt.Errorf("fmt chunk not found")
}

// extractWAVData 从 WAV 文件中提取音频 data chunk
func extractWAVData(data []byte) ([]byte, error) {
	if len(data) < 44 {
		return nil, fmt.Errorf("data too short")
	}
	if !looksLikeWAV(data) {
		return nil, fmt.Errorf("not a WAV file")
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

// buildWAVFile 构建 WAV 文件
func buildWAVFile(header *WAVHeader, audioData []byte) ([]byte, error) {
	var buf bytes.Buffer

	dataSize := uint32(len(audioData))
	fileSize := 36 + dataSize

	buf.WriteString("RIFF")
	if err := binary.Write(&buf, binary.LittleEndian, fileSize); err != nil {
		return nil, err
	}
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	if err := binary.Write(&buf, binary.LittleEndian, uint32(16)); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.AudioFormat); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.NumChannels); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.SampleRate); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.ByteRate); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.BlockAlign); err != nil {
		return nil, err
	}
	if err := binary.Write(&buf, binary.LittleEndian, header.BitsPerSample); err != nil {
		return nil, err
	}

	buf.WriteString("data")
	if err := binary.Write(&buf, binary.LittleEndian, dataSize); err != nil {
		return nil, err
	}
	if _, err := buf.Write(audioData); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// combineRawChunks 简单拼接音频块
func combineRawChunks(chunks [][]byte) ([]byte, error) {
	var buffer bytes.Buffer
	for _, chunk := range chunks {
		_, _ = buffer.Write(chunk)
	}
	return buffer.Bytes(), nil
}

// GetAudioDuration 估算音频时长（秒）
func GetAudioDuration(data []byte, format AudioFormat) (float64, error) {
	switch format {
	case FormatWAV:
		return getWAVDuration(data)
	case FormatMP3:
		return estimateMP3Duration(data)
	default:
		return 0, fmt.Errorf("unsupported format for duration calculation")
	}
}

func getWAVDuration(data []byte) (float64, error) {
	header, err := parseWAVHeader(data)
	if err != nil {
		return 0, err
	}

	audio, err := extractWAVData(data)
	if err != nil {
		return 0, err
	}

	if header.ByteRate == 0 {
		return 0, fmt.Errorf("invalid byte rate")
	}

	return float64(len(audio)) / float64(header.ByteRate), nil
}

func estimateMP3Duration(data []byte) (float64, error) {
	// 估算：假设 128kbps
	const bytesPerSecond = 128000.0 / 8.0
	if bytesPerSecond == 0 {
		return 0, fmt.Errorf("invalid bitrate")
	}
	return float64(len(data)) / bytesPerSecond, nil
}

// ValidateAudioData 粗略验证合并后的音频数据
func ValidateAudioData(data []byte, format AudioFormat) error {
	if len(data) == 0 {
		return fmt.Errorf("empty audio data")
	}

	switch format {
	case FormatMP3:
		return validateMP3(data)
	case FormatWAV:
		return validateWAV(data)
	default:
		return nil
	}
}

func validateMP3(data []byte) error {
	if len(data) < 4 {
		return fmt.Errorf("data too short for MP3")
	}

	// ID3v2
	if data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
		return nil
	}

	// MP3 frame sync: 0xFFEx
	if data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
		return nil
	}

	return fmt.Errorf("invalid MP3 data")
}

func validateWAV(data []byte) error {
	if len(data) < 12 {
		return fmt.Errorf("data too short for WAV")
	}
	if !looksLikeWAV(data) {
		return fmt.Errorf("invalid WAV header")
	}
	return nil
}

// AudioReader 音频流读取器
type AudioReader struct {
	reader io.Reader
	format AudioFormat
}

// NewAudioReader 创建音频读取器
func NewAudioReader(r io.Reader, format AudioFormat) *AudioReader {
	return &AudioReader{
		reader: r,
		format: format,
	}
}

// ReadAll 读取所有音频数据
func (ar *AudioReader) ReadAll() ([]byte, error) {
	return io.ReadAll(ar.reader)
}