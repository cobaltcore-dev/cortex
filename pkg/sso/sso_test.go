// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package sso

import (
	"net/http"
	"testing"

	"github.com/cobaltcore-dev/cortex/pkg/conf"
)

func TestNewHTTPClient(t *testing.T) {
	tests := []struct {
		name       string
		conf       conf.SSOConfig
		wantError  bool
		selfSigned bool
	}{
		{
			name: "NoCertProvided",
			conf: conf.SSOConfig{
				Cert:       "",
				CertKey:    "",
				SelfSigned: false,
			},
			wantError: false,
		},
		{
			name: "CertProvidedNoKey",
			conf: conf.SSOConfig{
				Cert:       "dummy-cert",
				CertKey:    "",
				SelfSigned: false,
			},
			wantError: true,
		},
		{
			name: "CertAndKeyProvided",
			conf: conf.SSOConfig{
				Cert:       "dummy-cert",
				CertKey:    "dummy-key",
				SelfSigned: false,
			},
			wantError: true, // No valid cert/key pair
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewHTTPClient(tt.conf); (err != nil) != tt.wantError {
				t.Errorf("NewHTTPClient() error = %v, wantError %v", err, tt.wantError)
				return
			}
		})
	}
}

func TestNewHTTPClientWithSelfSignedCert(t *testing.T) {
	// Dummy cert that was generated using openssl.
	dummyCert := `
-----BEGIN CERTIFICATE-----
MIIFOTCCAyGgAwIBAgIUK8JDK7gOYHBB9G5yug8Tl2MKxocwDQYJKoZIhvcNAQEL
BQAwRTELMAkGA1UEBhMCQVUxEzARBgNVBAgMClNvbWUtU3RhdGUxITAfBgNVBAoM
GEludGVybmV0IFdpZGdpdHMgUHR5IEx0ZDAeFw0yNTA1MDgwOTA2NDVaFw0yNjA1
MDgwOTA2NDVaMEUxCzAJBgNVBAYTAkFVMRMwEQYDVQQIDApTb21lLVN0YXRlMSEw
HwYDVQQKDBhJbnRlcm5ldCBXaWRnaXRzIFB0eSBMdGQwggIiMA0GCSqGSIb3DQEB
AQUAA4ICDwAwggIKAoICAQCFty3u3e91msBMxgu+sst37RReRS+brFRxcwEBF3p8
KU6jHm0Nnwe5KPgvaxEcwvaCRLg8cpXIVyiktQKqCBSFIkv4p/ASitKOPomDxAUk
G5YtbODa14ak1jk8HwHCzb0ydQt4RH2V2cbSHz0br1QucVhIcRYvAr4HCXiduEDf
bcu8F7Y1u2QH6wc+ul0bZcsZLR9fYR8smbakrvyd2C5OWdLUMOLLti3FITuzPq/i
Z0p3jP4oDAj/M2REH0k8xIS1xxFwINMvQIirteaxf5q2PcmTfjkJYwo2HLmE8vuH
JYSOUceltTxrPNV9qYP3iLxT6/kekkkK/yogJOdRejRXKVt4g0o/6Q3xwB7uXnmn
u3xHAlvw5jKJramDgQB5xmXZcdxEzFH4lQGlygV+ZxSJHPkP8cRwnlfxwfhUOmuj
mHylI1WexwVonHvA02bqW4ImHKr8YiHHiI/MOB+J+UJdcOeEM6zWHLIguLH0gj1Y
EbZ/RABSWhiflGLhUmFioWbv7U3mAQNUfLBu76CzNsxqkQ23SMaoe+9PU/LFc9zF
eXMUmvxnXPkJZksMr6DNFUhi7BiPYkEvBwbEIIFyQ8dklvn6FzjmCvvKw3NzLpiq
CNHs1PzHzHlXPQJKNiQl5W0QdfHeM2Nont8zX4nuHs4+N4+Zy8w7yhQMPAKa+YS4
hQIDAQABoyEwHzAdBgNVHQ4EFgQULqyYybe8azVtxivR258LC9Av1kgwDQYJKoZI
hvcNAQELBQADggIBAFIqsBHql63V4F25JOyDpWvz/RHZxmFA60Q4/IathKYUqldT
k7psLGJgDKPAwaxk+csYOLuz4BN/kEMYnJcRU6HKKL4RDs1nRhOjol7956jlXBgF
adhkmmfTHTWNcFt7/uKEezYZgTppgEiYobSgEjQsYWXRbcHcemipYR3C6JAYjFfo
terb/cGnZDTE2F13h+RyPOJzkj3fBRaPsf9cVlfookeXOp/1SkGvG8oo1Pe6nMCv
70xJskY/04oKy1hM463j2FQKtbG4/cpUmgrnUAt+rQjanTlZ4pnLQZj+4sLv15yr
mJ4YXP/v8IKmZK40rlxpNC6rs1TqUU/xxfWqiOIRcqrCCRPDUuhG3bDJIsLtFFsY
lS9t/a8wEULMQU+dxMMwIxofDCaH3dOX23PdEDmR06kI8/Y05rHUyANR+q8qL8el
M1pLkafK/lLmM9Xd/Dppvryow2KDWpkLNPX0OXT3QgpbQRcNc+6q7Zdu4HnnCAIq
Zyp6pwnslnsZui5j4NWHu+RcXX+Iurhza5rpRv/ozD9u4V0I8g6d907nPy87nZvD
nKwrFrgFDgGe6O4EfOhFj59w44sCa9MzY2WwJH5rqY66k1+2EGfXMiHexAEG+4zx
c6qsOW/E2r9OrDDih+SXKx6QvsKlYcOvi8p9XNg+l/Pg5xtUx/G99fCDYZot
-----END CERTIFICATE-----
	`
	dummyKey := `
-----BEGIN PRIVATE KEY-----
MIIJQwIBADANBgkqhkiG9w0BAQEFAASCCS0wggkpAgEAAoICAQCFty3u3e91msBM
xgu+sst37RReRS+brFRxcwEBF3p8KU6jHm0Nnwe5KPgvaxEcwvaCRLg8cpXIVyik
tQKqCBSFIkv4p/ASitKOPomDxAUkG5YtbODa14ak1jk8HwHCzb0ydQt4RH2V2cbS
Hz0br1QucVhIcRYvAr4HCXiduEDfbcu8F7Y1u2QH6wc+ul0bZcsZLR9fYR8smbak
rvyd2C5OWdLUMOLLti3FITuzPq/iZ0p3jP4oDAj/M2REH0k8xIS1xxFwINMvQIir
teaxf5q2PcmTfjkJYwo2HLmE8vuHJYSOUceltTxrPNV9qYP3iLxT6/kekkkK/yog
JOdRejRXKVt4g0o/6Q3xwB7uXnmnu3xHAlvw5jKJramDgQB5xmXZcdxEzFH4lQGl
ygV+ZxSJHPkP8cRwnlfxwfhUOmujmHylI1WexwVonHvA02bqW4ImHKr8YiHHiI/M
OB+J+UJdcOeEM6zWHLIguLH0gj1YEbZ/RABSWhiflGLhUmFioWbv7U3mAQNUfLBu
76CzNsxqkQ23SMaoe+9PU/LFc9zFeXMUmvxnXPkJZksMr6DNFUhi7BiPYkEvBwbE
IIFyQ8dklvn6FzjmCvvKw3NzLpiqCNHs1PzHzHlXPQJKNiQl5W0QdfHeM2Nont8z
X4nuHs4+N4+Zy8w7yhQMPAKa+YS4hQIDAQABAoICAAdSxlaUG86lGxhuqwCsFNi3
SKuhE7/C6x0LihKKkVAbiGMGrOesvg+LXuG59htNJ5Ma5bw/H6pJRms+6VZxUC5l
InlhOT91ZZ1vb2NNaRABMshqHiaI3Kb1Kff7pW0LN5bRMj+eq1d5sJnxfAyiyEmC
4QLsA/sEe26HO5k9GNB5LZ9aQnB5mDuWyQl2di2koEBQvax2Spl2vGS98Lf0ZGow
y+CIjONQpvu/m4rFntHCNnc1vEsVMv5HIkauL/n+tAYApm6DCGNIyt4cqEswYe4j
6YEFI2U64g70o8nURP0HNlX/xJ90EvW3RI9iqVz1RYXodqG7VqTI34Bt2lLBY/xj
J+ZSdbe4zw7/SUSuPyZgDWjnnHMAxjiZTsQGTGTeZUm7lAWlNprIb2ZpNdZISKGO
PnvR6vVu0Ju8U0BIrmWqBY5llCmvcd8xgp5KaGxTe6um4wbM1Myf3nknNwodhVmq
YRPLa+tHD78XzqeV6nZIVEfXmp0CUwg3JOBVBesBiiyUnjQPhIW0mE6p0dz0WccL
GwoobLTqa6WOC+9d6P9YgA2DMQ1cUKI6z12jAQDJtLwGBpQWjqWBt/OJuWi0H7NT
2NwJWTmfxn7zc7uf0LbhciG0jKO2GjnHb1IsLSAsTOcMU/hWnhJAb2KfEVUjH6AN
xrGG4cKbQq3CcVLRcU4BAoIBAQC8J397BHvGil/YzNDs3tuUSHslv9ebcFBzu2Lq
Ykr5OpXeXVyrxo6aBlETatdQPKoT0r5swvGYN0noiQ2QHgDnVLwfm1XhNCaDjZPf
SA5WSXN6TJYtsJuw+SKdnJ4kEstPnUTCtLZzvdO8jBqLDIhePMc+6U/ULGzR4L30
QWO8qrE9aOBPQiExOKkIllbBT3vS8YIpvlE4/FH2vFveDvTp22mT7gZO8ewUBedA
Te7QJ5yFlFMTFVfhAc45JeLvUCx7rzzXbtSjlMITXraEwCGc5Z+iTxNSEgXxNHNk
Rh4gv+jhUqW8tL9J/QmcTa9iSc2Yl/6iG2wArpFPgHcNNFB5AoIBAQC17nJlv+ed
p+khT4y9ygQgdz8kHvSXsxmwFeCz4Lb4kcTYgXtwUBLwvu6JObC7oMYzXqiZcZD3
PfQlNC14L7P4P6eOAXgwT0NgsbVYEE4MQkTmjVoK6dcQPD9zUWBJPeSxc3W0ssvD
LOtym9CCpXsOZSpt5sCN6GdJwL/C3KpL1LlB9GEmTTC+GxtWdMQ92hNKtF30s1Lz
ByblSNr/ty/uWf897yXm/bVrpGaxOkDJfS49P84zlqEbjMOSv88Hyf4I7MrJHSkb
UQ/3ZvdeylyF6EXf31OqGVjJEBoWxGyovIL5x5bOGCgbVA+gVpJ75HN4ESRGXZdS
VsM2XmOhdt1tAoIBAQCWbgnZI9uF/+njntVHHGJ4Kn7yzm+mIeTgsqfB9vY0Tue1
kfVejPBEKtq1aI1e5DGiibKfqDiaV1Hq7XB/kc1tJm0F5B6EYDqOoSnhsW1tBWqj
FApZ20KO+pD3bFlvQ+ty6q0n8m2RGeroaydploqMtZEjNkwRubcDEektGP6Rv/LW
wzvbgmahQMi8Sd5wzYiVPWuwzi2IHwu09iGI53JeaoL9t6cphPgXhiS+X9CYcaMN
lWnZ7w2Eovnq7OSEKxh1hsRhBYZShsOn0uigODBnjZrUnN44lppTn3jGadz6mBSr
2XUS63uovvrpEZ8wOQt8fcEigEQYQ3mAE5ibYQEZAoIBAQCkAbkPENjzvxLi/Jub
3CmsOtOo9F77AnH90zsl7UYE/yO9Kbzlmsn4Tacr/d3cxyrl1EeZTE+rEyatA0Sa
PCa5fGjIE3sN0eajnJAmO0ygsHz8eiDaBcPi1u08P/fVDv7DGZrasvQNlskKIHzv
yc4NRBXjzUl4pDG4wxIb0GGUysfXNT7/EEcImdcjMVBXkegiSEcK+T2l6KSfvfXu
4G1NKcR3SMeaXMzXpPUOf7035qlwfbydtQS3mUYVXOR92RIxaYXFl4wfHAyQszn9
MeAGt0WGdAUwKnlniCR8scZits477jl8wTomqLkNif2zwlZ1vr480NJBYAXLVXvr
awRhAoIBABIxLrOMn4ANOcgHTXstwO3uoLwjtUjJHJAwxTYQeTYfveEs7rd/UyxD
NfJvt0faJBOTQw8VWTaZVQNsA2IX3ksQnsH9a/EufHW5wZbuRZw6EPn6isoCZweS
LKkDvPKBjdm5lVaW9pRHARZE5r2EPN2ptlgjUvjtWbgE1ly+OYsaS30QhzB33AYt
+hs0BW70menC5k6fNHNkVDj8XCJgiCraEZM/fKXNKOZELoQT2Oo/Xg6IyCKFGAC9
boCBfqu7r37Ca6Tg4svExXA0N7xwDWKkI0MYi+3GablUwbPrOwxfloYBWM+s91Vd
nyCru8FaKdd+A5MBMSTb8MX0LcnWvdQ=
-----END PRIVATE KEY-----
	`

	conf := conf.SSOConfig{
		Cert:       dummyCert,
		CertKey:    dummyKey,
		SelfSigned: true,
	}

	client, err := NewHTTPClient(conf)
	if err != nil {
		t.Fatalf("NewHTTPClient() error = %v, want no error", err)
	}

	transport, ok := client.Transport.(*requestLogger)
	if !ok {
		t.Fatalf("Expected transport to be of type *requestLogger, got %T", client.Transport)
	}

	httpTransport, ok := transport.T.(*http.Transport)
	if !ok {
		t.Fatalf("Expected inner transport to be of type *http.Transport, got %T", transport.T)
	}

	if httpTransport.TLSClientConfig == nil {
		t.Fatal("Expected TLSClientConfig to be non-nil")
	}

	if !httpTransport.TLSClientConfig.InsecureSkipVerify {
		t.Error("Expected InsecureSkipVerify to be true for self-signed certificates")
	}
}
