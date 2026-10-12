package proxyutil

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// PublicProxyMarker is an internal routing policy, never sent to the proxy.
const PublicProxyMarker = "geili-public-proxy"
const PublicDirectMarkerGeili = "geili-public-direct"
const PublicDirectURLGeili = "http://public-upstream.invalid#geili-public-direct"

func PublicDialContextGeili(ctx context.Context, network, address string) (net.Conn, error) {
	return publicProxyDialer{}.DialContext(ctx, network, address)
}

const ExternalProxyNamePrefix = "[external] "

var publicProxyLookup = net.DefaultResolver.LookupNetIP

func IsPublicProxyIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() &&
		!(ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip)) &&
		!netip.MustParsePrefix("0.0.0.0/8").Contains(ip) && !netip.MustParsePrefix("192.88.99.0/24").Contains(ip) &&
		!netip.MustParsePrefix("100.64.0.0/10").Contains(ip) && !netip.MustParsePrefix("192.0.0.0/24").Contains(ip) &&
		!netip.MustParsePrefix("192.0.2.0/24").Contains(ip) && !netip.MustParsePrefix("198.18.0.0/15").Contains(ip) &&
		!netip.MustParsePrefix("198.51.100.0/24").Contains(ip) && !netip.MustParsePrefix("203.0.113.0/24").Contains(ip) &&
		!netip.MustParsePrefix("240.0.0.0/4").Contains(ip) && !netip.MustParsePrefix("2001:db8::/32").Contains(ip) &&
		!netip.MustParsePrefix("2001::/23").Contains(ip) && !netip.MustParsePrefix("3fff::/20").Contains(ip) &&
		!netip.MustParsePrefix("64:ff9b::/96").Contains(ip) && !netip.MustParsePrefix("2002::/16").Contains(ip)
}

func publicProxyAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	addresses, err := publicProxyLookup(ctx, "ip", host)
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("proxy address unavailable")
	}
	for _, ip := range addresses {
		if !IsPublicProxyIP(ip) {
			return nil, errors.New("proxy must use public addresses")
		}
	}
	return addresses, nil
}
func ValidatePublicProxyHost(ctx context.Context, host string) error {
	_, err := publicProxyAddresses(ctx, host)
	return err
}

type publicProxyDialer struct{}

func (publicProxyDialer) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return publicProxyDialer{}.DialContext(ctx, network, address)
}
func (publicProxyDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("invalid proxy address")
	}
	addresses, err := publicProxyAddresses(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range addresses {
		conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
	}
	return nil, errors.New("proxy connection unavailable")
}

func configurePublicProxyGeili(t *http.Transport, u *url.URL) (bool, error) {
	if u.Fragment == PublicDirectMarkerGeili {
		t.Proxy = nil
		t.DialContext = publicProxyDialer{}.DialContext
		return true, nil
	}
	if u.Fragment != PublicProxyMarker {
		return false, nil
	}
	clean := *u
	clean.Fragment = ""
	clean.RawFragment = ""
	if clean.Scheme == "http" || clean.Scheme == "https" {
		t.Proxy = http.ProxyURL(&clean)
		t.DialContext = publicProxyDialer{}.DialContext
		return true, nil
	}
	d, err := proxy.FromURL(&clean, publicProxyDialer{})
	if err != nil {
		return true, errors.New("invalid public proxy")
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return true, errors.New("public proxy context unavailable")
	}
	// The SOCKS dialer owns the single proxy hop, including WebSocket clients.
	t.Proxy = nil
	t.DialContext = cd.DialContext
	return true, nil
}
