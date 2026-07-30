# NCP dedicated Root terminal profile. Loaded only by the NCP PTY.
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

# ble.sh provides interactive syntax highlighting, history search and completion.
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

# Signal readiness after the interactive editor has attached. The descriptor is
# private to the Agent and never becomes part of terminal input or output.
if [[ ${NCP_TERMINAL_READY_FD:-} == 3 ]]; then
  printf 'ready\n' >&3 || true
  exec 3>&-
fi
