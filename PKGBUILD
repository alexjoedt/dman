# Maintainer: Alex Joedt <alex@joedt.com>
pkgname=dman
pkgver=1.0.0
pkgrel=1
pkgdesc="A dotfile manager focused on Git overlay model and snapshots"
arch=('x86_64' 'aarch64')
url="https://github.com/alexjoedt/dman"
license=('MIT')
depends=('glibc')
makedepends=('go' 'git')
source=("$pkgname-$pkgver.tar.gz::https://github.com/alexjoedt/$pkgname/archive/refs/tags/v$pkgver.tar.gz")
sha256sums=('SKIP')

prepare() {
    cd "$pkgname-$pkgver"
    go mod download -modcacherw
}

build() {
    cd "$pkgname-$pkgver"
    export CGO_CPPFLAGS="${CPPFLAGS}"
    export CGO_CFLAGS="${CFLAGS}"
    export CGO_CXXFLAGS="${CXXFLAGS}"
    export CGO_LDFLAGS="${LDFLAGS}"
    export GOFLAGS="-buildmode=pie -trimpath -mod=readonly -modcacherw"
    go build \
        -ldflags "-s -w -linkmode=external -X main.Version=v$pkgver" \
        -o build/dman \
        .
}

check() {
    cd "$pkgname-$pkgver"
    GOFLAGS="" go test ./...
}

package() {
    cd "$pkgname-$pkgver"
    install -Dm755 build/dman "$pkgdir/usr/bin/dman"
    install -Dm644 LICENSE "$pkgdir/usr/share/licenses/$pkgname/LICENSE"
    install -Dm644 README.md "$pkgdir/usr/share/doc/$pkgname/README.md"
}
