set shell := ["bash", "-euo", "pipefail", "-c"]

up:
    bash hack/smoke/up.sh

down:
    bash hack/smoke/down.sh
