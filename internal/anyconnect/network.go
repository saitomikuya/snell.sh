package anyconnect

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/proxy-panel/proxy-panel/internal/nodes"
)

const (
	maxAssetDownload         = 8 << 20
	maxCiscoStaticIPv4Routes = 1200
	maxChinaCIDRRoutes       = maxCiscoStaticIPv4Routes - 3
	downloadAttempts         = 2
	downloadRetryDelay       = 750 * time.Millisecond
	chinaCIDRIANAMirror      = "https://ftp.iana.org/pub/mirror/rirstats/apnic/delegated-apnic-latest"
	chinaCIDRRIPEMirror      = "https://ftp.ripe.net/pub/stats/apnic/delegated-apnic-latest"
)

var nonPublicDownloadRanges = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.88.99.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func safeHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 15 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, fmt.Errorf("resolve certificate or CIDR host: %w", err)
			}
			// Some VPS providers advertise IPv6 while routing it incompletely. Try
			// IPv4 first so a broken IPv6 path does not consume the whole request
			// deadline before the working address is attempted.
			sort.SliceStable(addresses, func(i, j int) bool {
				return addresses[i].Is4() && !addresses[j].Is4()
			})
			for _, address := range addresses {
				if !isPublicAddress(address) {
					return nil, errors.New("下载地址解析到了内网、回环、链路本地或保留 IP")
				}
			}
			var dialErrors []error
			for _, address := range addresses {
				connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if dialErr == nil {
					return connection, nil
				}
				dialErrors = append(dialErrors, dialErr)
			}
			return nil, errors.Join(dialErrors...)
		},
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("下载重定向次数过多")
			}
			if len(via) > 0 && !strings.EqualFold(req.URL.Hostname(), via[0].URL.Hostname()) {
				return errors.New("下载重定向不能离开原始主机")
			}
			return nil
		},
	}
}

func isPublicAddress(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsUnspecified() {
		return false
	}
	for _, prefix := range nonPublicDownloadRanges {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func download(ctx context.Context, client *http.Client, rawURL, username, password string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return nil, errors.New("下载地址必须是无内嵌凭据的 HTTPS URL")
	}
	var lastErr error
	for attempt := 0; attempt < downloadAttempts; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if requestErr != nil {
			return nil, requestErr
		}
		request.Header.Set("Accept", "application/x-pem-file,text/plain,application/octet-stream")
		request.Header.Set("User-Agent", "ProxyPanel-AnyConnect/1")
		if username != "" || password != "" {
			request.SetBasicAuth(username, password)
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			lastErr = requestErr
		} else {
			if response.StatusCode != http.StatusOK {
				response.Body.Close()
				lastErr = fmt.Errorf("下载返回 HTTP %d", response.StatusCode)
				// Client errors will not be fixed by retrying the same URL.
				if response.StatusCode < 500 {
					return nil, lastErr
				}
			} else if response.ContentLength > maxAssetDownload {
				response.Body.Close()
				return nil, errors.New("下载内容超过 8 MiB 限制")
			} else {
				content, readErr := io.ReadAll(io.LimitReader(response.Body, maxAssetDownload+1))
				response.Body.Close()
				if readErr != nil {
					lastErr = readErr
				} else if len(content) == 0 || len(content) > maxAssetDownload {
					return nil, errors.New("下载内容为空或超过 8 MiB 限制")
				} else {
					return content, nil
				}
			}
		}
		if attempt+1 < downloadAttempts {
			timer := time.NewTimer(downloadRetryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}

func chinaCIDRSourceURLs(source string) []string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = nodes.DefaultChinaCIDRSource
	}
	if source != nodes.DefaultChinaCIDRSource {
		return []string{source}
	}
	return []string{source, chinaCIDRIANAMirror, chinaCIDRRIPEMirror}
}

func downloadChinaCIDRs(ctx context.Context, client *http.Client, source string) ([]byte, string, error) {
	urls := chinaCIDRSourceURLs(source)
	errs := make([]error, 0, len(urls))
	for _, candidate := range urls {
		content, err := download(ctx, client, candidate, "", "")
		if err == nil {
			return content, candidate, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", candidate, err))
	}
	return nil, "", errors.Join(errs...)
}

func ParseChinaCIDRs(input []byte, format string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	var err error
	switch format {
	case "apnic":
		prefixes, err = parseAPNIC(input)
	case "cidr":
		prefixes, err = parseCIDRList(input)
	default:
		return nil, errors.New("不支持的 CIDR 数据格式")
	}
	if err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		if prefix.Bits() < 7 || !prefix.Addr().Is4() || !isPublicAddress(prefix.Addr()) {
			return nil, fmt.Errorf("中国 CIDR 包含过宽或非公网前缀：%s", prefix)
		}
	}
	prefixes = excludeSpecialUsePrefixes(prefixes)
	prefixes = normalizePrefixes(prefixes)
	if len(prefixes) < 100 {
		return nil, fmt.Errorf("中国 CIDR 条目数量异常：%d", len(prefixes))
	}
	var addressCount uint64
	for index, prefix := range prefixes {
		if prefix.Bits() < 7 || !isPublicAddress(prefix.Addr()) {
			return nil, fmt.Errorf("中国 CIDR 包含过宽或非公网前缀：%s", prefix)
		}
		for _, blocked := range nonPublicDownloadRanges {
			if blocked.Addr().BitLen() == prefix.Addr().BitLen() && (prefix.Contains(blocked.Addr()) || blocked.Contains(prefix.Addr())) {
				return nil, fmt.Errorf("中国 CIDR 与特殊用途地址范围重叠：%s", prefix)
			}
		}
		if index > 0 && prefixes[index-1].Contains(prefix.Addr()) {
			return nil, fmt.Errorf("中国 CIDR 包含重叠前缀：%s", prefix)
		}
		addressCount += uint64(1) << (32 - prefix.Bits())
	}
	if addressCount < 10_000_000 || addressCount > 1_000_000_000 {
		return nil, fmt.Errorf("中国 CIDR 地址总量异常：%d", addressCount)
	}
	if format == "apnic" {
		prefixes = collapsePrefixes(prefixes)
		prefixes = largestPrefixes(prefixes, maxChinaCIDRRoutes)
	} else if len(prefixes) > maxChinaCIDRRoutes {
		return nil, fmt.Errorf("中国 CIDR 共 %d 条，超过为 Cisco Secure Client 预留 DNS 直连路由后的上限 %d；请改用已聚合的 CIDR/no-route 数据源", len(prefixes), maxChinaCIDRRoutes)
	}
	return prefixes, nil
}

func parseAPNIC(input []byte) ([]netip.Prefix, error) {
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var prefixes []netip.Prefix
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 7 || !strings.EqualFold(fields[1], "CN") || fields[2] != "ipv4" || (fields[6] != "allocated" && fields[6] != "assigned") {
			continue
		}
		start, parseErr := netip.ParseAddr(fields[3])
		count, countErr := strconv.ParseUint(fields[4], 10, 64)
		if parseErr != nil || countErr != nil || !start.Is4() || count == 0 || count > 1<<32 {
			return nil, fmt.Errorf("APNIC IPv4 记录无效：%s", line)
		}
		blocks, rangeErr := rangeToPrefixes(start, count)
		if rangeErr != nil {
			return nil, rangeErr
		}
		prefixes = append(prefixes, blocks...)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return prefixes, nil
}

func parseCIDRList(input []byte) ([]netip.Prefix, error) {
	scanner := bufio.NewScanner(bytes.NewReader(input))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var prefixes []netip.Prefix
	for scanner.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.Trim(line, "'\"")
		if line == "" || line == "payload:" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "no-route") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) != "no-route" {
				return nil, fmt.Errorf("CIDR 数据包含无效的 no-route 配置：%s", line)
			}
			line = strings.TrimSpace(parts[1])
		}
		prefix, err := parseIPv4Prefix(line)
		if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || !isPublicAddress(prefix.Addr()) {
			return nil, fmt.Errorf("CIDR 数据包含无效的公网 IPv4 前缀：%s", line)
		}
		prefixes = append(prefixes, prefix)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return prefixes, nil
}

func parseIPv4Prefix(value string) (netip.Prefix, error) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !strings.Contains(parts[1], ".") {
		return netip.ParsePrefix(value)
	}
	address, err := netip.ParseAddr(parts[0])
	if err != nil || !address.Is4() {
		return netip.Prefix{}, errors.New("invalid IPv4 address")
	}
	maskIP := net.ParseIP(parts[1]).To4()
	if maskIP == nil {
		return netip.Prefix{}, errors.New("invalid IPv4 netmask")
	}
	ones, bits := net.IPMask(maskIP).Size()
	if bits != 32 || ones < 0 {
		return netip.Prefix{}, errors.New("non-contiguous IPv4 netmask")
	}
	return netip.PrefixFrom(address, ones), nil
}

func excludeSpecialUsePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	result := append([]netip.Prefix(nil), prefixes...)
	for _, excluded := range nonPublicDownloadRanges {
		if !excluded.Addr().Is4() {
			continue
		}
		next := make([]netip.Prefix, 0, len(result))
		for _, prefix := range result {
			next = append(next, subtractPrefix(prefix, excluded)...)
		}
		result = next
	}
	return result
}

func subtractPrefix(prefix, excluded netip.Prefix) []netip.Prefix {
	if !prefix.Contains(excluded.Addr()) && !excluded.Contains(prefix.Addr()) {
		return []netip.Prefix{prefix}
	}
	if excluded.Bits() <= prefix.Bits() && excluded.Contains(prefix.Addr()) {
		return nil
	}
	nextBits := prefix.Bits() + 1
	left := netip.PrefixFrom(prefix.Addr(), nextBits)
	raw := prefix.Addr().As4()
	rightValue := binary.BigEndian.Uint32(raw[:]) + uint32(uint64(1)<<(32-nextBits))
	binary.BigEndian.PutUint32(raw[:], rightValue)
	right := netip.PrefixFrom(netip.AddrFrom4(raw), nextBits)
	result := subtractPrefix(left, excluded)
	return append(result, subtractPrefix(right, excluded)...)
}

func rangeToPrefixes(start netip.Addr, count uint64) ([]netip.Prefix, error) {
	bytes4 := start.As4()
	current := uint64(binary.BigEndian.Uint32(bytes4[:]))
	if current+count > 1<<32 {
		return nil, errors.New("IPv4 地址范围越界")
	}
	var result []netip.Prefix
	remaining := count
	for remaining > 0 {
		block := uint64(1)
		for block < remaining && block < 1<<32 && current%(block<<1) == 0 && block<<1 <= remaining {
			block <<= 1
		}
		bits := 32
		for value := block; value > 1; value >>= 1 {
			bits--
		}
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(current))
		result = append(result, netip.PrefixFrom(netip.AddrFrom4(raw), bits))
		current += block
		remaining -= block
	}
	return result, nil
}

func normalizePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	seen := make(map[string]bool, len(prefixes))
	result := make([]netip.Prefix, 0, len(prefixes))
	for _, prefix := range prefixes {
		prefix = prefix.Masked()
		key := prefix.String()
		if !seen[key] {
			seen[key] = true
			result = append(result, prefix)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i].Addr().As4(), result[j].Addr().As4()
		leftValue, rightValue := binary.BigEndian.Uint32(left[:]), binary.BigEndian.Uint32(right[:])
		if leftValue == rightValue {
			return result[i].Bits() < result[j].Bits()
		}
		return leftValue < rightValue
	})
	return result
}

func collapsePrefixes(prefixes []netip.Prefix) []netip.Prefix {
	prefixes = normalizePrefixes(prefixes)
	type interval struct {
		start uint64
		end   uint64
	}
	intervals := make([]interval, 0, len(prefixes))
	for _, prefix := range prefixes {
		raw := prefix.Addr().As4()
		start := uint64(binary.BigEndian.Uint32(raw[:]))
		end := start + uint64(1)<<(32-prefix.Bits())
		if len(intervals) > 0 && start == intervals[len(intervals)-1].end {
			intervals[len(intervals)-1].end = end
			continue
		}
		intervals = append(intervals, interval{start: start, end: end})
	}
	result := make([]netip.Prefix, 0, len(prefixes))
	for _, item := range intervals {
		var raw [4]byte
		binary.BigEndian.PutUint32(raw[:], uint32(item.start))
		collapsed, err := rangeToPrefixes(netip.AddrFrom4(raw), item.end-item.start)
		if err == nil {
			result = append(result, collapsed...)
		}
	}
	return normalizePrefixes(result)
}

func largestPrefixes(prefixes []netip.Prefix, limit int) []netip.Prefix {
	if len(prefixes) <= limit {
		return prefixes
	}
	result := append([]netip.Prefix(nil), prefixes...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Bits() == result[j].Bits() {
			left, right := result[i].Addr().As4(), result[j].Addr().As4()
			return binary.BigEndian.Uint32(left[:]) < binary.BigEndian.Uint32(right[:])
		}
		return result[i].Bits() < result[j].Bits()
	})
	return normalizePrefixes(result[:limit])
}

func renderNoRoutes(prefixes []netip.Prefix) []byte {
	var output strings.Builder
	for _, prefix := range prefixes {
		mask := net.CIDRMask(prefix.Bits(), 32)
		output.WriteString("no-route = ")
		output.WriteString(prefix.Addr().String())
		output.WriteByte('/')
		output.WriteString(net.IP(mask).String())
		output.WriteByte('\n')
	}
	return []byte(output.String())
}

func fingerprint(input []byte) string {
	value := sha256.Sum256(input)
	return hex.EncodeToString(value[:])
}
