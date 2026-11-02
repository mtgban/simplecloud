package simplecloud

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"sync"

	xzReader "github.com/xi2/xz"
)

// xzAhead is how many decoded pieces may wait for the reader: blocks in
// flight in parallel, or chunks of a stream decoded serially. A JSON
// decoder, the usual consumer, keeps pace with about three blocks.
const xzAhead = 4

// xzChunkSize is the piece a serially decoded stream is handed over in.
const xzChunkSize = 1 << 20

var (
	xzHeaderMagic = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	xzFooterMagic = []byte{'Y', 'Z'}
	// errXZFormat stands for xi2's ErrFormat on the framing this file
	// reads itself, and wraps it so callers can match either.
	errXZFormat = fmt.Errorf("simplecloud: xz framing: %w", xzReader.ErrFormat)
)

// xzPiece is one decoded piece, in stream order, or the error that ended
// decoding there.
type xzPiece struct {
	data []byte
	err  error
}

// xzParallelReader decodes an xz file ahead of its reader, in a goroutine
// of its own. A stream whose block headers record their compressed and
// uncompressed sizes, as multi-threaded xz writes them, has its blocks
// decoded concurrently. Any other stream is decoded serially by xi2. Each
// block still goes through xi2, as a stream of one block, so its check and
// xi2's 64 MiB dictionary cap apply as before; the stream's own index and
// footer are checked against the blocks seen.
//
// A stream that records sizes in its first block header but not in a
// later one is refused. xz-utils writes sizes in every header or none.
type xzParallelReader struct {
	pieces chan chan xzPiece
	cur    []byte
	held   []byte
	err    error

	// free holds the buffers of pieces read through, for later pieces to
	// fill; a catalog's worth of blocks is otherwise garbage.
	free chan []byte

	done      chan struct{}
	exited    chan struct{}
	closeOnce sync.Once
}

// newXZReader checks the first stream header now, so a file that is not
// xz fails here as it did with xi2, and decodes the rest in the background.
func newXZReader(r io.Reader) (*xzParallelReader, error) {
	src := bufio.NewReader(r)
	header := make([]byte, 12)
	_, err := io.ReadFull(src, header)
	if err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	err = checkStreamHeader(header)
	if err != nil {
		return nil, err
	}

	z := &xzParallelReader{
		pieces: make(chan chan xzPiece, xzAhead),
		free:   make(chan []byte, xzAhead+2),
		done:   make(chan struct{}),
		exited: make(chan struct{}),
	}
	go z.produce(src, header)
	return z, nil
}

func (z *xzParallelReader) Read(p []byte) (int, error) {
	for len(z.cur) == 0 {
		if z.held != nil {
			z.recycle(z.held)
			z.held = nil
		}
		if z.err != nil {
			return 0, z.err
		}
		next, ok := <-z.pieces
		if !ok {
			z.err = io.EOF
			continue
		}
		piece := <-next
		z.cur, z.held, z.err = piece.data, piece.data, piece.err
	}
	n := copy(p, z.cur)
	z.cur = z.cur[n:]
	return n, nil
}

// Close stops decoding and waits for the goroutine reading the source to
// finish, so the source can be closed after it.
func (z *xzParallelReader) Close() error {
	z.closeOnce.Do(func() {
		close(z.done)
		<-z.exited
		z.err = io.ErrClosedPipe
		z.cur = nil
	})
	return nil
}

func (z *xzParallelReader) recycle(buf []byte) {
	select {
	case z.free <- buf[:0]:
	default:
	}
}

// buffer returns an empty buffer with room for size bytes, reusing one
// read through when it is big enough.
func (z *xzParallelReader) buffer(size int) []byte {
	select {
	case buf := <-z.free:
		if cap(buf) >= size {
			return buf
		}
	default:
	}
	return make([]byte, 0, size)
}

// send queues a piece and reports whether the reader still wants more.
func (z *xzParallelReader) send(piece chan xzPiece) bool {
	select {
	case z.pieces <- piece:
		return true
	case <-z.done:
		return false
	}
}

func (z *xzParallelReader) fail(err error) {
	piece := make(chan xzPiece, 1)
	piece <- xzPiece{err: err}
	z.send(piece)
}

// produce walks the streams one after another, starting from the stream
// header the constructor read.
func (z *xzParallelReader) produce(src *bufio.Reader, header []byte) {
	defer close(z.exited)
	defer close(z.pieces)

	for {
		serial, err := z.stream(src, header)
		if err != nil {
			z.fail(err)
			return
		}
		if serial != nil {
			z.serial(serial)
			return
		}

		header, err = nextStreamHeader(src)
		if err != nil {
			z.fail(err)
			return
		}
		if header == nil {
			return
		}
	}
}

// stream decodes one stream's blocks in parallel. When its first block
// header records no sizes it decodes nothing and returns the input from
// this stream's header onwards, for xi2 to decode serially instead.
func (z *xzParallelReader) stream(src *bufio.Reader, header []byte) (io.Reader, error) {
	checkSize := xzCheckSize(header[7] & 0x0f)
	type record struct{ unpadded, uncompressed uint64 }
	var records []record

	for {
		first, err := src.ReadByte()
		if err != nil {
			return nil, unexpected(err)
		}
		if first == 0x00 {
			break
		}

		blockHeader := make([]byte, (int(first)+1)*4)
		blockHeader[0] = first
		_, err = io.ReadFull(src, blockHeader[1:])
		if err != nil {
			return nil, unexpected(err)
		}
		compressed, uncompressed, sized, err := blockSizes(blockHeader)
		if err != nil {
			return nil, err
		}
		if !sized {
			if len(records) == 0 {
				consumed := append(append([]byte{}, header...), blockHeader...)
				return io.MultiReader(bytes.NewReader(consumed), src), nil
			}
			return nil, fmt.Errorf("%w: block %d records no sizes, the ones before it did", errXZFormat, len(records))
		}

		// Read what the block holds rather than allocating what its
		// header declares, which costs nothing to inflate.
		block := bytes.NewBuffer(blockHeader)
		padded := (compressed + 3) &^ 3
		_, err = io.CopyN(block, src, int64(padded+checkSize))
		if err != nil {
			return nil, unexpected(err)
		}
		unpadded := uint64(len(blockHeader)) + compressed + checkSize
		records = append(records, record{unpadded, uncompressed})

		piece := make(chan xzPiece, 1)
		if !z.send(piece) {
			return nil, io.ErrClosedPipe
		}
		// Sized from the block's own bytes as well as its header, which a
		// small file can inflate; ReadFrom wants bytes.MinRead spare.
		size := min(uncompressed, 64*uint64(block.Len()), 64<<20)
		buf := z.buffer(int(size) + bytes.MinRead)
		go decodeBlock(piece, buf, header, block.Bytes(), unpadded, uncompressed)
	}

	// The index: its indicator byte is already read.
	index := &crcByteReader{r: src, crc: crc32.ChecksumIEEE([]byte{0x00}), n: 1}
	count, err := binary.ReadUvarint(index)
	if err != nil {
		return nil, unexpected(err)
	}
	if count != uint64(len(records)) {
		return nil, fmt.Errorf("%w: index lists %d blocks, the stream holds %d", errXZFormat, count, len(records))
	}
	for i, want := range records {
		unpadded, err := binary.ReadUvarint(index)
		if err != nil {
			return nil, unexpected(err)
		}
		uncompressed, err := binary.ReadUvarint(index)
		if err != nil {
			return nil, unexpected(err)
		}
		if unpadded != want.unpadded || uncompressed != want.uncompressed {
			return nil, fmt.Errorf("%w: index record %d does not match its block", errXZFormat, i)
		}
	}
	for index.n%4 != 0 {
		b, err := index.ReadByte()
		if err != nil {
			return nil, unexpected(err)
		}
		if b != 0 {
			return nil, fmt.Errorf("%w: index padding", errXZFormat)
		}
	}
	indexSize := index.n + 4 // and its CRC32
	sum := index.crc
	tail := make([]byte, 4+12)
	_, err = io.ReadFull(src, tail)
	if err != nil {
		return nil, unexpected(err)
	}
	if binary.LittleEndian.Uint32(tail[:4]) != sum {
		return nil, fmt.Errorf("%w: index CRC32", errXZFormat)
	}
	return nil, checkStreamFooter(tail[4:], header, indexSize)
}

// serial decodes the rest of the input with xi2 alone, handing it over in
// chunks so the reader still works alongside it.
func (z *xzParallelReader) serial(src io.Reader) {
	dec, err := xzReader.NewReader(src, 0)
	if err != nil {
		z.fail(err)
		return
	}
	for {
		buf := z.buffer(xzChunkSize)[:xzChunkSize]
		n, err := io.ReadFull(dec, buf)
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			err = nil
			if n == 0 {
				return
			}
		}
		piece := make(chan xzPiece, 1)
		piece <- xzPiece{data: buf[:n], err: err}
		if !z.send(piece) || err != nil {
			return
		}
	}
}

// decodeBlock decodes one block into buf by handing xi2 a stream holding
// it alone: the file's stream header, the block, and an index and footer
// naming it.
func decodeBlock(piece chan<- xzPiece, buf, header, block []byte, unpadded, uncompressed uint64) {
	index := []byte{0x00}
	index = binary.AppendUvarint(index, 1)
	index = binary.AppendUvarint(index, unpadded)
	index = binary.AppendUvarint(index, uncompressed)
	for len(index)%4 != 0 {
		index = append(index, 0x00)
	}
	index = binary.LittleEndian.AppendUint32(index, crc32.ChecksumIEEE(index))

	footer := make([]byte, 4, 12)
	footer = binary.LittleEndian.AppendUint32(footer, uint32(len(index)/4-1))
	footer = append(footer, header[6:8]...)
	binary.LittleEndian.PutUint32(footer, crc32.ChecksumIEEE(footer[4:10]))
	footer = append(footer, xzFooterMagic...)

	stream := io.MultiReader(bytes.NewReader(header), bytes.NewReader(block),
		bytes.NewReader(index), bytes.NewReader(footer))
	dec, err := xzReader.NewReader(stream, 0)
	if err != nil {
		piece <- xzPiece{err: err}
		return
	}
	dec.Multistream(false)
	out := bytes.NewBuffer(buf)
	_, err = out.ReadFrom(dec)
	piece <- xzPiece{data: out.Bytes(), err: err}
}

// blockSizes reads the sizes a block header records, after checking its
// CRC32. sized is false unless it records both.
func blockSizes(h []byte) (compressed, uncompressed uint64, sized bool, err error) {
	body := h[:len(h)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(h[len(h)-4:]) {
		return 0, 0, false, fmt.Errorf("%w: block header CRC32", errXZFormat)
	}
	flags := body[1]
	if flags&0xc0 != 0xc0 {
		return 0, 0, false, nil
	}
	rest := body[2:]
	compressed, n := binary.Uvarint(rest)
	if n <= 0 || n > 9 || compressed == 0 || compressed > 1<<62 {
		return 0, 0, false, fmt.Errorf("%w: block compressed size", errXZFormat)
	}
	uncompressed, m := binary.Uvarint(rest[n:])
	if m <= 0 || m > 9 {
		return 0, 0, false, fmt.Errorf("%w: block uncompressed size", errXZFormat)
	}
	return compressed, uncompressed, true, nil
}

func checkStreamHeader(h []byte) error {
	if !bytes.Equal(h[:6], xzHeaderMagic) {
		return fmt.Errorf("%w: stream header magic", errXZFormat)
	}
	if h[6] != 0 || h[7]&0xf0 != 0 {
		return fmt.Errorf("%w: stream flags", errXZFormat)
	}
	if crc32.ChecksumIEEE(h[6:8]) != binary.LittleEndian.Uint32(h[8:12]) {
		return fmt.Errorf("%w: stream header CRC32", errXZFormat)
	}
	return nil
}

func checkStreamFooter(f, header []byte, indexSize int) error {
	if !bytes.Equal(f[10:12], xzFooterMagic) {
		return fmt.Errorf("%w: stream footer magic", errXZFormat)
	}
	if crc32.ChecksumIEEE(f[4:10]) != binary.LittleEndian.Uint32(f[:4]) {
		return fmt.Errorf("%w: stream footer CRC32", errXZFormat)
	}
	if (int(binary.LittleEndian.Uint32(f[4:8]))+1)*4 != indexSize {
		return fmt.Errorf("%w: stream footer backward size", errXZFormat)
	}
	if !bytes.Equal(f[8:10], header[6:8]) {
		return fmt.Errorf("%w: stream footer flags", errXZFormat)
	}
	return nil
}

// nextStreamHeader skips stream padding and reads the header of the stream
// after it, or returns nil at the end of the input.
func nextStreamHeader(src *bufio.Reader) ([]byte, error) {
	word := make([]byte, 4)
	for {
		_, err := io.ReadFull(src, word)
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		if err != nil {
			return nil, unexpected(err)
		}
		if !bytes.Equal(word, []byte{0, 0, 0, 0}) {
			break
		}
	}
	header := make([]byte, 12)
	copy(header, word)
	_, err := io.ReadFull(src, header[4:])
	if err != nil {
		return nil, unexpected(err)
	}
	return header, checkStreamHeader(header)
}

// xzCheckSize is the size of the check a block carries for a check type.
func xzCheckSize(check byte) uint64 {
	if check == 0 {
		return 0
	}
	return 4 << ((check - 1) / 3)
}

// unexpected reports input that ends partway through the framing.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// crcByteReader counts and checksums the bytes read through it.
type crcByteReader struct {
	r   io.ByteReader
	crc uint32
	n   int
}

func (c *crcByteReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.crc = crc32.Update(c.crc, crc32.IEEETable, []byte{b})
		c.n++
	}
	return b, err
}
