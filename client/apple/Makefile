# Alvos do cliente Apple. O núcleo Swift (KurzKit) é testado com SwiftPM, sem Xcode.
.PHONY: test-kit test-kit-unit

# Testes unitários do KurzKit (o e2e fica SKIPPED sem KURZ_E2E_URL).
test-kit-unit:
	KurzKit/Scripts/swift-test.sh

# Sobe o servidor Go + Postgres e roda TUDO, inclusive o e2e (precisa de Docker e Go).
test-kit:
	KurzKit/Scripts/e2e.sh
