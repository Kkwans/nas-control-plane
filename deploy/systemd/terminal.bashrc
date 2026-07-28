# NCP 专用 Root 终端配置。仅由 NCP PTY 加载，不修改 /root/.bashrc。
export HISTFILE=/root/.ncp_bash_history
export HISTCONTROL=ignoredups:erasedups
export HISTSIZE=2000
export HISTFILESIZE=4000
export TERM=xterm-256color
export COLORTERM=truecolor
export CLICOLOR=1
export LS_COLORS='di=38;5;31:ln=38;5;67:so=38;5;97:pi=38;5;130:ex=38;5;28:bd=38;5;166:cd=38;5;166:su=38;5;160:sg=38;5;160:tw=38;5;31:ow=38;5;31'
shopt -s histappend checkwinsize
alias ls='ls --color=auto'
alias grep='grep --color=auto'
alias egrep='egrep --color=auto'
alias fgrep='fgrep --color=auto'
alias diff='diff --color=auto'

PS1='\[\e[38;5;25m\]\u@\h\[\e[0m\]:\[\e[38;5;30m\]\w\[\e[0m\]# '

# ble.sh 提供交互式语法高亮、历史搜索与补全。部署包未安装时安全回退原生 Bash。
if [[ -r /opt/ncp/share/blesh/ble.sh ]]; then
  source /opt/ncp/share/blesh/ble.sh --attach=none
  ble-face -s command_builtin fg=25,bold
  ble-face -s command_file fg=30,bold
  ble-face -s syntax_varname fg=97
  ble-face -s syntax_quoted fg=28
  ble-face -s syntax_comment fg=244
  ble-face -s syntax_error fg=160,underline
  ble-face -s argument_option fg=130
  ble-face -s filename_directory fg=31,underline
  ble-face -s filename_executable fg=28
  ble-face -s varname_array fg=97
  ble-attach
fi
