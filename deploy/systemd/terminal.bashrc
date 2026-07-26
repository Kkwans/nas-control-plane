# NCP 专用 Root 终端配置。仅由 NCP PTY 加载，不修改 /root/.bashrc。
export HISTFILE=/root/.ncp_bash_history
export HISTCONTROL=ignoredups:erasedups
export HISTSIZE=2000
export HISTFILESIZE=4000
shopt -s histappend checkwinsize

PS1='\[\e[38;5;25m\]\u@\h\[\e[0m\]:\[\e[38;5;30m\]\w\[\e[0m\]# '

# ble.sh 提供交互式语法高亮、历史搜索与补全。部署包未安装时安全回退原生 Bash。
if [[ -r /opt/ncp/share/blesh/ble.sh ]]; then
  source /opt/ncp/share/blesh/ble.sh --attach=none
  ble-face -s command_builtin fg=25
  ble-face -s command_file fg=30
  ble-face -s syntax_error fg=160
  ble-face -s filename_directory fg=31
  ble-face -s varname_array fg=97
  ble-attach
fi
