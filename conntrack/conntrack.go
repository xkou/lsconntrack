package conntrack

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"

	gnet "github.com/shirou/gopsutil/net"
)

type ConnMode int

const (
	ConnOther ConnMode = iota
	ConnActive
	ConnPassive
)

// RawConnStat represents statistics of a connection to other host and port.
type RawConnStat struct {
	OriginalSaddr   string
	OriginalDaddr   string
	OriginalSport   string
	OriginalDport   string
	OriginalPackets int64
	OriginalBytes   int64
	ReplySaddr      string
	ReplyDaddr      string
	ReplySport      string
	ReplyDport      string
	ReplyPackets    int64
	ReplyBytes      int64
}

type ConnStatByAddrPort map[string]*ConnStat

type ConnStatEntries struct {
	Active  ConnStatByAddrPort `json:"active"`
	Passive ConnStatByAddrPort `json:"passive"`
}

// ConnStat represents statistics of a connection to localhost or from localhost.
type ConnStat struct {
	Mode                 ConnMode
	Addr                 string `json:"addr"`
	Port                 string `json:"port"`
	TotalInboundPackets  int64  `json:"total_inbound_packets"`
	TotalInboundBytes    int64  `json:"total_inbound_bytes"`
	TotalOutboundPackets int64  `json:"total_outbound_packets"`
	TotalOutboundBytes   int64  `json:"total_outbound_bytes"`
}

func parseRawConnStat(rstat *RawConnStat, localAddrs []string, ports []string) *ConnStat {
	var (
		mode       ConnMode
		addr, port string
	)
	for _, localAddr := range localAddrs {
		if rstat.OriginalSaddr == localAddr && contains(ports, rstat.OriginalDport) {
			mode, addr, port = ConnActive, rstat.OriginalDaddr, rstat.OriginalDport
			break
		}
		if rstat.ReplyDaddr == localAddr && contains(ports, rstat.ReplySport) {
			mode, addr, port = ConnActive, rstat.ReplySaddr, rstat.ReplySport
			break
		}
		if rstat.OriginalDaddr == localAddr && contains(ports, rstat.OriginalDport) {
			mode, addr, port = ConnPassive, rstat.OriginalSaddr, rstat.OriginalDport // not OriginalSport
			break
		}
		if rstat.ReplySaddr == localAddr && contains(ports, rstat.ReplySport) {
			mode, addr, port = ConnPassive, rstat.ReplyDaddr, rstat.ReplySport // not ReplyDport
			break
		}
	}
	switch mode {
	case ConnOther:
		return nil
	case ConnActive:
		return &ConnStat{
			Mode:                 ConnActive,
			Addr:                 addr,
			Port:                 port,
			TotalInboundPackets:  rstat.ReplyPackets,
			TotalInboundBytes:    rstat.ReplyBytes,
			TotalOutboundPackets: rstat.OriginalPackets,
			TotalOutboundBytes:   rstat.OriginalBytes,
		}
	case ConnPassive:
		return &ConnStat{
			Mode:                 ConnPassive,
			Addr:                 addr,
			Port:                 port,
			TotalInboundPackets:  rstat.OriginalPackets,
			TotalInboundBytes:    rstat.OriginalBytes,
			TotalOutboundPackets: rstat.ReplyPackets,
			TotalOutboundBytes:   rstat.ReplyBytes,
		}
	}
	return nil
}

func (c *ConnStatEntries) insert(stat *ConnStat) {
	key := net.JoinHostPort(stat.Addr, stat.Port)
	switch stat.Mode {
	case ConnActive:
		if _, ok := c.Active[key]; !ok {
			c.Active[key] = stat
			return
		}
		c.Active[key].TotalInboundPackets += stat.TotalInboundPackets
		c.Active[key].TotalInboundBytes += stat.TotalInboundBytes
		c.Active[key].TotalOutboundPackets += stat.TotalOutboundPackets
		c.Active[key].TotalOutboundBytes += stat.TotalOutboundBytes
	case ConnPassive:
		if _, ok := c.Passive[key]; !ok {
			c.Passive[key] = stat
			return
		}
		c.Passive[key].TotalInboundPackets += stat.TotalInboundPackets
		c.Passive[key].TotalInboundBytes += stat.TotalInboundBytes
		c.Passive[key].TotalOutboundPackets += stat.TotalOutboundPackets
		c.Passive[key].TotalOutboundBytes += stat.TotalOutboundBytes
	}
	return
}

// ParseEntries parses '/proc/net/nf_conntrack or /proc/net/ip_conntrack'.
func ParseEntries(r io.Reader, ports []string) (*ConnStatEntries, error) {
	localAddrs, err := localIPaddrs()
	if err != nil {
		return nil, err
	}
	entries := &ConnStatEntries{
		Active:  ConnStatByAddrPort{},
		Passive: ConnStatByAddrPort{},
	}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		rstat := parseLine(line)
		if rstat == nil {
			continue
		}
		stat := parseRawConnStat(rstat, localAddrs, ports)
		if stat == nil {
			continue
		}
		entries.insert(stat)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

func localIPaddrs() ([]string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	addrStrings := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				addrStrings = append(addrStrings, ipnet.IP.String())
			}
		}
	}
	return addrStrings, nil
}

// LocalListeningPorts returns the local listening ports.
// eg. [199, 111, 46131, 53, 8953, 25, 2812, 80, 8081, 22]
// -----------------------------------------------------------------------------------
// [y_uuki@host ~]$ netstat -tln
// Active Internet connections (only servers)
// Proto Recv-Q Send-Q Local Address               Foreign Address             State
// tcp        0      0 0.0.0.0:199                 0.0.0.0:*                   LISTEN
// tcp        0      0 0.0.0.0:111                 0.0.0.0:*                   LISTEN
// tcp        0      0 0.0.0.0:46131               0.0.0.0:*                   LISTEN
// tcp        0      0 127.0.0.1:53                0.0.0.0:*                   LISTEN
// tcp        0      0 127.0.0.1:8953              0.0.0.0:*                   LISTEN
// tcp        0      0 127.0.0.1:25                0.0.0.0:*                   LISTEN
// tcp        0      0 0.0.0.0:2812                0.0.0.0:*                   LISTEN
// tcp        0      0 :::80                       :::*                        LISTEN
// tcp        0      0 :::8081                     :::*                        LISTEN
// tcp        0      0 :::22                       :::*                        LISTEN
func LocalListeningPorts() ([]string, error) {
	conns, err := gnet.Connections("tcp")
	if err != nil {
		return nil, err
	}
	ports := []string{}
	for _, conn := range conns {
		if conn.Laddr.IP == "0.0.0.0" || conn.Laddr.IP == "127.0.0.1" || conn.Laddr.IP == "::" {
			ports = append(ports, fmt.Sprintf("%d", conn.Laddr.Port))
		}
	}
	return ports, nil
}

func parseLine(line string) *RawConnStat {
	stat := &RawConnStat{}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		log.Fatalf("unexpected line: %s\n", line)
	}
	if fields[0] != "tcp" {
		return nil
	}
	if strings.Contains(line, "[UNREPLIED]") {
		// tcp      6 367755 ESTABLISHED src=10.0.0.1 dst=10.0.0.2 sport=3306 dport=38205 packets=1 bytes=52 [UNREPLIED] src=10.0.0.2 dst=10.0.0.1 sport=38205 dport=3306 packets=0 bytes=0 mark=0 secmark=0 use=1
		stat.OriginalSaddr = strings.Split(fields[4], "=")[1]
		stat.OriginalDaddr = strings.Split(fields[5], "=")[1]
		stat.OriginalSport = strings.Split(fields[6], "=")[1]
		stat.OriginalDport = strings.Split(fields[7], "=")[1]
		stat.OriginalPackets, _ = strconv.ParseInt(strings.Split(fields[8], "=")[1], 10, 64)
		stat.OriginalBytes, _ = strconv.ParseInt(strings.Split(fields[9], "=")[1], 10, 64)
		stat.ReplySaddr = strings.Split(fields[11], "=")[1]
		stat.ReplyDaddr = strings.Split(fields[12], "=")[1]
		stat.ReplySport = strings.Split(fields[13], "=")[1]
		stat.ReplyDport = strings.Split(fields[14], "=")[1]
		stat.ReplyPackets, _ = strconv.ParseInt(strings.Split(fields[15], "=")[1], 10, 64)
		stat.ReplyBytes, _ = strconv.ParseInt(strings.Split(fields[16], "=")[1], 10, 64)
		return stat
	} else if strings.Contains(line, "[ASSURED]") {
		// tcp      6 5 CLOSE src=10.0.0.10 dst=10.0.0.11 sport=41143 dport=443 packets=3 bytes=164 src=10.0.0.11 dst=10.0.0.10 sport=443 dport=41143 packets=1 bytes=60 [ASSURED] mark=0 secmark=0 use=1
		stat.OriginalSaddr = strings.Split(fields[4], "=")[1]
		stat.OriginalDaddr = strings.Split(fields[5], "=")[1]
		stat.OriginalSport = strings.Split(fields[6], "=")[1]
		stat.OriginalDport = strings.Split(fields[7], "=")[1]
		stat.OriginalPackets, _ = strconv.ParseInt(strings.Split(fields[8], "=")[1], 10, 64)
		stat.OriginalBytes, _ = strconv.ParseInt(strings.Split(fields[9], "=")[1], 10, 64)
		stat.ReplySaddr = strings.Split(fields[10], "=")[1]
		stat.ReplyDaddr = strings.Split(fields[11], "=")[1]
		stat.ReplySport = strings.Split(fields[12], "=")[1]
		stat.ReplyDport = strings.Split(fields[13], "=")[1]
		stat.ReplyPackets, _ = strconv.ParseInt(strings.Split(fields[14], "=")[1], 10, 64)
		stat.ReplyBytes, _ = strconv.ParseInt(strings.Split(fields[15], "=")[1], 10, 64)
		return stat
	}
	return nil
}
