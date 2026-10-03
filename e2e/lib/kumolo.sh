# Shared helpers for e2e runners that start their own kumolo instance.
# Source this file; do not execute it.

port_in_use() {
  (exec 3<>"/dev/tcp/localhost/$1") 2>/dev/null
}

# pick_free_port MIN RANGE [COUNT]
# Prints a random port in [MIN, MIN+RANGE) such that it and the COUNT-1 ports
# after it are all free.
pick_free_port() {
  local min=$1 range=$2 count=${3:-1} attempt port i busy
  for ((attempt = 0; attempt < 20; attempt++)); do
    port=$(((RANDOM % range) + min))
    busy=0
    for ((i = 0; i < count; i++)); do
      if port_in_use $((port + i)); then
        busy=1
        break
      fi
    done
    if [[ $busy -eq 0 ]]; then
      echo "$port"
      return 0
    fi
  done
  echo "ERROR: no free port found in [$min, $((min + range)))" >&2
  return 1
}

# require_free_port PORT
# Fails when something already listens on PORT, so a readiness probe can never
# be answered by a foreign process while the launched kumolo is still on its
# way to a failing bind.
require_free_port() {
  local port=$1
  if port_in_use "$port"; then
    echo "ERROR: port $port is already in use"
    exit 1
  fi
}

# wait_kumolo_ready PID PORT LOG_FILE PROBE...
# Waits until PROBE succeeds while PID is still alive.
wait_kumolo_ready() {
  local pid=$1 port=$2 log=$3 n ready
  shift 3
  for ((n = 0; n < 40; n++)); do
    ready=0
    "$@" >/dev/null 2>&1 && ready=1
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "ERROR: kumolo exited before becoming ready (port $port)"
      cat "$log"
      exit 1
    fi
    if [[ $ready -eq 1 ]]; then
      return 0
    fi
    sleep 0.25
  done
  echo "ERROR: kumolo did not start in time (port $port)"
  cat "$log"
  exit 1
}
