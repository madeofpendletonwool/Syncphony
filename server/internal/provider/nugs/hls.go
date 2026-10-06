// SPDX-License-Identifier: AGPL-3.0-only

package nugs

import (
	"bufio"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/madeofpendletonwool/syncphony/server/internal/provider"
)

const (
	// maxPlaylist caps an HLS playlist.
	maxPlaylist = 4 << 20
	// maxSegment caps one HLS segment. They're a few seconds of AAC.
	maxSegment = 64 << 20
)

// hlsStream plays an HLS rendition as one continuous stream: the best
// variant's segments in order, decrypted when they're AES-128 encrypted.
// nugs.net serves some tracks only this way, as AAC in MPEG-TS, which the
// server's transcoder turns into something a browser plays. It can't seek.
func (p *Provider) hlsStream(ctx context.Context, playlistURL string) (*provider.AudioStream, error) {
	segs, err := p.mediaSegments(ctx, playlistURL)
	if err != nil {
		return nil, err
	}
	ct := "video/mp2t"
	if u, err := url.Parse(segs[0].url); err == nil && path.Ext(u.Path) == ".aac" {
		ct = "audio/aac"
	}
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(p.writeSegments(ctx, segs, pw)) }()
	return &provider.AudioStream{Body: &hlsBody{PipeReader: pr, cancel: cancel}, ContentType: ct, Length: -1, Size: -1}, nil
}

// hlsBody stops fetching segments when it's closed.
type hlsBody struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (b *hlsBody) Close() error {
	b.cancel()
	return b.PipeReader.Close()
}

// mediaSegments reads a playlist, following a master playlist to its
// highest-bandwidth variant.
func (p *Provider) mediaSegments(ctx context.Context, playlistURL string) ([]segment, error) {
	for range 2 {
		pl, err := p.playlist(ctx, playlistURL)
		if err != nil {
			return nil, err
		}
		if len(pl.variants) == 0 {
			if len(pl.segments) == 0 {
				return nil, fmt.Errorf("nugs hls: %w: empty playlist", provider.ErrUnavailable)
			}
			return pl.segments, nil
		}
		best := pl.variants[0]
		for _, v := range pl.variants[1:] {
			if v.bandwidth > best.bandwidth {
				best = v
			}
		}
		playlistURL = best.url
	}
	return nil, fmt.Errorf("nugs hls: %w: master playlist points to another", provider.ErrUnavailable)
}

func (p *Provider) playlist(ctx context.Context, rawURL string) (*playlist, error) {
	body, err := p.fetch(ctx, rawURL, "hls playlist", maxPlaylist)
	if err != nil {
		return nil, err
	}
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return parsePlaylist(string(body), base)
}

// fetch GETs a small file whole.
func (p *Provider) fetch(ctx context.Context, rawURL, what string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := newRequest(ctx, rawURL, "")
	if err != nil {
		return nil, err
	}
	resp, err := p.do(req, what, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("nugs %s: %w: %w", what, provider.ErrUnavailable, err)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("nugs %s: over %d bytes", what, limit)
	}
	return b, nil
}

func (p *Provider) writeSegments(ctx context.Context, segs []segment, w io.Writer) error {
	keys := map[string][]byte{}
	for _, sg := range segs {
		data, err := p.fetch(ctx, sg.url, "hls segment", maxSegment)
		if err != nil {
			return err
		}
		if sg.key != nil {
			k, ok := keys[sg.key.uri]
			if !ok {
				if k, err = p.fetch(ctx, sg.key.uri, "hls key", 1<<10); err != nil {
					return err
				}
				if len(k) < aes.BlockSize {
					return fmt.Errorf("nugs hls: %w: short key", provider.ErrUnavailable)
				}
				k = k[:aes.BlockSize]
				keys[sg.key.uri] = k
			}
			iv := sg.key.iv
			if iv == nil {
				// The IV defaults to the segment's sequence number.
				iv = make([]byte, aes.BlockSize)
				binary.BigEndian.PutUint64(iv[8:], uint64(sg.seq)) //nolint:gosec // sequence numbers aren't negative
			}
			if data, err = decryptSegment(k, iv, data); err != nil {
				return err
			}
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// decryptSegment undoes HLS's AES-128: CBC with PKCS#7 padding.
func decryptSegment(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("nugs hls: %w: segment isn't whole blocks", provider.ErrUnavailable)
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize {
		return nil, fmt.Errorf("nugs hls: %w: bad padding", provider.ErrUnavailable)
	}
	return out[:len(out)-pad], nil
}

type playlist struct {
	variants []variant
	segments []segment
}

type variant struct {
	url       string
	bandwidth int
}

type segment struct {
	url string
	// key is nil for an unencrypted segment.
	key *segmentKey
	seq int64
}

type segmentKey struct {
	uri string
	// iv is nil when the playlist doesn't give one.
	iv []byte
}

// errUnsupportedHLS is a playlist this reader can't play. Retrying won't
// help.
var errUnsupportedHLS = fmt.Errorf("nugs hls: unsupported playlist: %w", provider.ErrNotPlayable)

// parsePlaylist reads the parts of an HLS playlist that nugs.net uses.
func parsePlaylist(body string, base *url.URL) (*playlist, error) {
	pl := &playlist{}
	var (
		seq         int64
		key         *segmentKey
		nextVariant = -1 // the coming URI's bandwidth, if it's a variant
	)
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), maxPlaylist)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		tag, value, _ := strings.Cut(line, ":")
		switch {
		case line == "":
		case tag == "#EXT-X-STREAM-INF":
			nextVariant, _ = strconv.Atoi(attrs(value)["BANDWIDTH"])
			nextVariant = max(nextVariant, 0)
		case tag == "#EXT-X-MEDIA-SEQUENCE":
			seq, _ = strconv.ParseInt(value, 10, 64)
		case tag == "#EXT-X-KEY":
			a := attrs(value)
			switch a["METHOD"] {
			case "NONE":
				key = nil
			case "AES-128":
				u, err := resolve(base, a["URI"])
				if err != nil {
					return nil, err
				}
				key = &segmentKey{uri: u}
				if iv := a["IV"]; iv != "" {
					b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(iv, "0x"), "0X"))
					if err != nil || len(b) != 16 {
						return nil, fmt.Errorf("%w: bad IV", errUnsupportedHLS)
					}
					key.iv = b
				}
			default:
				return nil, fmt.Errorf("nugs hls: %s encryption: %w", a["METHOD"], provider.ErrNotPlayable)
			}
		case tag == "#EXT-X-MAP", tag == "#EXT-X-BYTERANGE":
			return nil, fmt.Errorf("%w: %s", errUnsupportedHLS, tag)
		case strings.HasPrefix(line, "#"):
		default:
			u, err := resolve(base, line)
			if err != nil {
				return nil, err
			}
			if nextVariant >= 0 {
				pl.variants = append(pl.variants, variant{url: u, bandwidth: nextVariant})
				nextVariant = -1
				continue
			}
			pl.segments = append(pl.segments, segment{url: u, key: key, seq: seq})
			seq++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", errUnsupportedHLS, err)
	}
	return pl, nil
}

// resolve makes a playlist's URI absolute. Only http(s) is allowed.
func resolve(base *url.URL, ref string) (string, error) {
	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("%w: bad URI", errUnsupportedHLS)
	}
	u := base.ResolveReference(r)
	if u.Scheme != "https" && u.Scheme != "http" {
		return "", fmt.Errorf("%w: URI scheme %q", errUnsupportedHLS, u.Scheme)
	}
	return u.String(), nil
}

// attrs parses an HLS attribute list: KEY=VALUE,KEY="quoted, value".
func attrs(s string) map[string]string {
	out := map[string]string{}
	for s != "" {
		k, rest, ok := strings.Cut(s, "=")
		if !ok {
			break
		}
		var v string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				v, rest = rest[1:], ""
			} else {
				v, rest = rest[1:1+end], rest[2+end:]
			}
			rest = strings.TrimPrefix(rest, ",")
		} else {
			v, rest, _ = strings.Cut(rest, ",")
		}
		out[strings.TrimSpace(k)] = v
		s = rest
	}
	return out
}
