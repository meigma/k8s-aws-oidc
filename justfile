set shell := ["bash", "-euo", "pipefail", "-c"]

up:
    bash hack/smoke/up.sh

down:
    bash hack/smoke/down.sh

failover:
    bash hack/smoke/failover.sh
