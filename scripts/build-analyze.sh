#!/bin/bash
# 兼容旧入口：编译 Mole 的 analyze。用法同 build-mole.sh 去掉第一个参数。
exec "$(dirname "${BASH_SOURCE[0]}")/build-mole.sh" analyze "$@"
