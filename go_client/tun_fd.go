package main

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// recvTunFD слушает unix-сокет sockPath и получает ровно один файловый
// дескриптор, переданный Android-стороной через SCM_RIGHTS (см.
// LocalSocket.setFileDescriptorsForSend в TunFdBridge.kt). Нужен, потому что
// go_client — отдельный OS-процесс (не JNI/in-process), поэтому TUN, который
// Android создаёт через VpnService.Builder().establish(), нельзя передать
// иначе как через межпроцессную передачу дескриптора.
//
// go_client — сервер (слушает), Android — клиент (подключается после
// establish()), а не наоборот. Раньше было наоборот (Android слушал,
// go_client дозванивался Dial-ом с ретраями) — это создавало гонку: go_client
// стартует раньше Android успевает поднять TUN и создать LocalServerSocket,
// первая попытка почти всегда получала connection refused. Инверсия убирает
// гонку полностью — go_client уже слушает задолго до того, как Android вообще
// начнёт establish().
func recvTunFD(sockPath string) (*os.File, error) {
	rawDiagf("recvTunFD: listen unix %q", sockPath)
	addr, err := net.ResolveUnixAddr("unix", sockPath)
	if err != nil {
		rawDiagf("recvTunFD: ResolveUnixAddr FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock resolve: %w", err)
	}
	ln, err := net.ListenUnix("unix", addr)
	if err != nil {
		rawDiagf("recvTunFD: ListenUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock listen: %w", err)
	}
	defer ln.Close()
	rawDiagf("recvTunFD: listening, waiting for Android to connect...")

	uc, err := ln.AcceptUnix()
	if err != nil {
		rawDiagf("recvTunFD: AcceptUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock accept: %w", err)
	}
	defer uc.Close()
	rawDiagf("recvTunFD: accept OK, waiting for SCM_RIGHTS...")

	buf := make([]byte, 4)
	oob := make([]byte, unix.CmsgSpace(4))
	_, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		rawDiagf("recvTunFD: ReadMsgUnix FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock read: %w", err)
	}
	rawDiagf("recvTunFD: ReadMsgUnix OK, oobn=%d", oobn)

	scms, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		rawDiagf("recvTunFD: ParseSocketControlMessage FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock parse cmsg: %w", err)
	}
	if len(scms) == 0 {
		rawDiagf("recvTunFD: 0 control messages received")
		return nil, fmt.Errorf("tun-fd-sock: контрольное сообщение не получено")
	}
	fds, err := unix.ParseUnixRights(&scms[0])
	if err != nil {
		rawDiagf("recvTunFD: ParseUnixRights FAILED: %v", err)
		return nil, fmt.Errorf("tun-fd-sock parse rights: %w", err)
	}
	if len(fds) == 0 {
		rawDiagf("recvTunFD: 0 fds in rights message")
		return nil, fmt.Errorf("tun-fd-sock: fd не получен")
	}

	rawDiagf("recvTunFD: received fd=%d", fds[0])
	return os.NewFile(uintptr(fds[0]), "tun"), nil
}
