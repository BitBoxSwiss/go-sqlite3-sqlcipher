#!/bin/sh
set -eu

if [ $# -eq 1 ]; then
  VERSION="$1"
else
  VERSION=$(git ls-remote --tags --refs --sort='v:refname' \
    https://github.com/sqlcipher/sqlcipher.git 'refs/tags/v[0-9]*' \
    | awk -F/ '{print $NF}' \
    | tail -n1)
fi

if [ -z "$VERSION" ]; then
  echo "Could not determine latest SQLCipher version" >&2
  exit 1
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
ROOT_DIR="$SCRIPT_DIR/../.."
IMAGE="sqlcipher-amalgamation:${VERSION}"
LIBTOMCRYPT_DIR="$ROOT_DIR/internal/libtomcrypt/src"
SQLCIPHER_ANDROID_REPO="https://github.com/sqlcipher/sqlcipher-android.git"

docker build -t "$IMAGE" --build-arg version="$VERSION" "$SCRIPT_DIR"

for ext in c h
do
  file="$SCRIPT_DIR/../../sqlcipher-binding.${ext}"
  {
    echo '#ifdef USE_SQLCIPHER'
    docker run --rm "$IMAGE" cat "/sqlcipher/sqlite3.${ext}"
    echo '#endif // USE_SQLCIPHER'
  } >"$file"
done

if ! git ls-remote --exit-code --tags --refs \
  "$SQLCIPHER_ANDROID_REPO" "refs/tags/$VERSION" >/dev/null
then
  echo "Could not find sqlcipher-android tag $VERSION" >&2
  exit 1
fi

TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

git -C "$TMP_DIR" init -q
git -C "$TMP_DIR" remote add origin "$SQLCIPHER_ANDROID_REPO"
git -C "$TMP_DIR" fetch --depth 1 origin "refs/tags/$VERSION"

LIBTOMCRYPT_COMMIT=$(
  git -C "$TMP_DIR" ls-tree FETCH_HEAD sqlcipher/src/main/jni/libtomcrypt/src |
    awk '{print $3}'
)

if [ -z "$LIBTOMCRYPT_COMMIT" ]; then
  echo "Could not determine LibTomCrypt submodule commit for $VERSION" >&2
  exit 1
fi

git -C "$ROOT_DIR" submodule update --init internal/libtomcrypt/src
git -C "$LIBTOMCRYPT_DIR" fetch --depth 1 origin "$LIBTOMCRYPT_COMMIT"
git -C "$LIBTOMCRYPT_DIR" checkout "$LIBTOMCRYPT_COMMIT"

(cd "$ROOT_DIR" && go run ./upgrade/sqlcipher/generate_libtomcrypt.go)
