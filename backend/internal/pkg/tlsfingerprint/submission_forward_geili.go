package tlsfingerprint

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/proxyutil"
	"net"
	"time"
)

type submissionPublicForwardDialerGeili struct{}

func (submissionPublicForwardDialerGeili) Dial(network, address string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return proxyutil.PublicDialContextGeili(ctx, network, address)
}
func (submissionPublicForwardDialerGeili) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return proxyutil.PublicDialContextGeili(ctx, network, address)
}
