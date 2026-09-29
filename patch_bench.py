# -*- coding: utf-8 -*-
import io

p = 'bin/run-bench.sh'
s = io.open(p, encoding='utf-8').read()

def rep(old, new):
    global s
    assert old in s, 'NOT FOUND: ' + repr(old[:100])
    s = s.replace(old, new)

rep('''run_suite() {
  local BIN="$1" LABEL="$2"
  cleanup
  echo "=== $LABEL ===" | tee -a "$RESULTS"

  sudo -n taskset -c 0 "$BIN" \\
    -listen 127.0.0.1:56000 \\
    -wg-port 56001 \\
    -config-dir "$CFG" \\
    -password-file "$CFG/main.password" \\
    -listen-raw 0.0.0.0:56003 \\
    > "$DIR/server-$LABEL.log" 2>&1 &''',
'''run_suite() {
  local BIN="$1" LABEL="$2" RAWPORT="$3" CIPHER="$4"
  cleanup
  echo "=== $LABEL ===" | tee -a "$RESULTS"

  local RAWARGS="-listen-raw 0.0.0.0:56003"
  if [ "$CIPHER" = "aes" ]; then
    # AES-сервер слушает и старый chacha-порт (совместимость), и новый aes
    RAWARGS="-listen-raw 0.0.0.0:56003 -listen-raw-aes 0.0.0.0:46000"
  fi

  sudo -n taskset -c 0 "$BIN" \\
    -listen 127.0.0.1:56000 \\
    -wg-port 56001 \\
    -config-dir "$CFG" \\
    -password-file "$CFG/main.password" \\
    $RAWARGS \\
    > "$DIR/server-$LABEL.log" 2>&1 &''')

rep('''    OUT=$("$DIR/loadgen" \\
      -server 127.0.0.1:56003 \\
      -password "$PASS" \\
      -device "bench-$LABEL-$PPS" \\
      -echo "$ECHO_ADDR" \\
      -pps "$PPS" -size 1200 -duration "$DURATION" 2>&1 | grep '^RESULT')''',
'''    OUT=$("$DIR/loadgen" \\
      -server 127.0.0.1:$RAWPORT \\
      -cipher "$CIPHER" \\
      -password "$PASS" \\
      -device "bench-$LABEL-$PPS" \\
      -echo "$ECHO_ADDR" \\
      -pps "$PPS" -size 1200 -duration "$DURATION" 2>&1 | grep '^RESULT')''')

rep(''': > "$RESULTS"
run_suite "$DIR/wdtt-srv-base" base
run_suite "$DIR/wdtt-srv-opt" opt1
run_suite "$DIR/wdtt-srv-opt2" opt2
run_suite "$DIR/wdtt-srv-opt3" opt3
echo "=== DONE ==="''',
''': > "$RESULTS"
run_suite "$DIR/wdtt-srv-base" base 56003 chacha
run_suite "$DIR/wdtt-srv-opt3" opt3 56003 chacha
run_suite "$DIR/wdtt-srv-opt4" opt4-chacha 56003 chacha
run_suite "$DIR/wdtt-srv-opt4" opt4-aes 46000 aes
echo "=== DONE ==="''')

io.open(p, 'w', encoding='utf-8', newline='').write(s)
print('bench script updated')
