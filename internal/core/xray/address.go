package xray

import (
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
)

func address(host string) *conf.Address {
	return &conf.Address{Address: net.ParseAddress(host)}
}

func tcpDestination(host string, port uint16) net.Destination {
	return net.TCPDestination(net.ParseAddress(host), net.Port(port))
}
