// Package sigcheck 提取 PE 文件的 Authenticode 数字签名者名称。
//
// 实现逻辑与 SoftCnKiller 一致：读取文件签名（signer）的显示名，
// 供上层与 sign.txt 发布者黑名单比对，识别来自已知流氓软件厂商的文件。
// 这里只读取签名者名称用于比对，不强制做完整证书链/吊销验证
// （与 softcnkiller 的行为对齐，识别快、不依赖网络）。
package sigcheck

import (
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	crypt32 = syscall.NewLazyDLL("crypt32.dll")

	procCryptQueryObject            = crypt32.NewProc("CryptQueryObject")
	procCertEnumCertificatesInStore = crypt32.NewProc("CertEnumCertificatesInStore")
	procCertGetNameStringW          = crypt32.NewProc("CertGetNameStringW")
	procCertCloseStore              = crypt32.NewProc("CertCloseStore")
)

// wincrypt.h 常量
const (
	certQueryObjectFile       = 1
	certQueryContentFlagAll   = 0x0000FFFF
	certQueryFormatFlagAll    = 0x0000FFFF
	certNameSimpleDisplayType = 4 // CERT_NAME_SIMPLE_DISPLAY_TYPE
	certNameIssuerFlag        = 1 // CERT_NAME_ISSUER_FLAG
)

// certName 取证书的 subject（或 issuer，当 flags=CERT_NAME_ISSUER_FLAG）显示名
func certName(cert uintptr, flags uint32) string {
	// CertGetNameStringW(cert, type, flags, pvTypePara=nil, buf, len)
	n, _, _ := procCertGetNameStringW.Call(
		cert, uintptr(certNameSimpleDisplayType), uintptr(flags), 0, 0, 0,
	)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n)
	procCertGetNameStringW.Call(
		cert, uintptr(certNameSimpleDisplayType), uintptr(flags), 0,
		uintptr(unsafe.Pointer(&buf[0])), n,
	)
	return strings.TrimSpace(syscall.UTF16ToString(buf))
}

// Signer 返回文件的 Authenticode 签名者显示名；未签名或读取失败返回错误。
//
// CryptQueryObject 打开文件的证书存储后，store 中可能同时包含
// 签名者叶证书与其签发 CA。判定签名者：叶证书的 subject 不会被
// store 中任何证书当作 issuer 引用；CA 的 subject 则会被其下级证书
// 引用，从而被排除。
func Signer(path string) (string, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	var hStore, hMsg uintptr
	r1, _, _ := procCryptQueryObject.Call(
		uintptr(certQueryObjectFile),
		uintptr(unsafe.Pointer(p)),
		uintptr(certQueryContentFlagAll),
		uintptr(certQueryFormatFlagAll),
		0, 0, 0, 0,
		uintptr(unsafe.Pointer(&hStore)),
		uintptr(unsafe.Pointer(&hMsg)),
		0,
	)
	if r1 == 0 || hStore == 0 {
		return "", fmt.Errorf("文件无数字签名或无法读取")
	}
	defer procCertCloseStore.Call(hStore, 0)

	// 枚举 store 内全部证书，收集 subject 名集合与 issuer 名集合
	var certs []uintptr
	issuers := map[string]bool{}
	var prev uintptr
	for {
		cert, _, _ := procCertEnumCertificatesInStore.Call(hStore, prev)
		if cert == 0 {
			break
		}
		prev = cert
		certs = append(certs, cert)
		if iss := certName(cert, certNameIssuerFlag); iss != "" {
			issuers[strings.ToLower(iss)] = true
		}
	}
	if len(certs) == 0 {
		return "", fmt.Errorf("文件无数字签名")
	}
	// 叶证书 = subject 未被任何证书当作 issuer
	for _, cert := range certs {
		subj := certName(cert, 0)
		if subj == "" {
			continue
		}
		if issuers[strings.ToLower(subj)] {
			continue // CA/中间证书：被下级证书签发引用
		}
		return subj, nil
	}
	// 兜底：取第一个有名字的证书（罕见链不完整场景）
	for _, cert := range certs {
		if subj := certName(cert, 0); subj != "" {
			return subj, nil
		}
	}
	return "", fmt.Errorf("签名者证书无显示名")
}
