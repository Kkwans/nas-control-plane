# NCP dedicated Root terminal profile. Loaded only by the NCP PTY.
export HISTFILE=/root/.ncp_bash_history
export HISTCONTROL=ignoredups:erasedups
export HISTSIZE=2000
export HISTFILESIZE=4000
export TERM=xterm-256color
export COLORTERM=truecolor
export CLICOLOR=1
export SYSTEMD_COLORS=1
export SYSTEMD_PAGER=cat
export GIT_PAGER=cat
export LESS='-R'
export LS_COLORS='di=38;5;31:ln=38;5;67:so=38;5;97:pi=38;5;130:ex=38;5;28:bd=38;5;166:cd=38;5;166:su=38;5;160:sg=38;5;160:tw=38;5;31:ow=38;5;31'
shopt -s histappend checkwinsize
alias ls='ls --color=auto'
alias grep='grep --color=auto'
alias egrep='egrep --color=auto'
alias fgrep='fgrep --color=auto'
alias diff='diff --color=auto'
if command -v git >/dev/null 2>&1; then
  alias git='git -c color.ui=always'
fi

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

  # ble.sh 的默认多行提示使用 RET/C-m/C-j 缩写；NCP 终端直接显示用户
  # 可按下的按键名称，避免把 C 误解为普通字符。
  bleopt keymap_emacs_mode_string_multiline=$'\e[1m多行粘贴模式\e[m'
  function ble/prompt/backslash:keymap:emacs/mode-indicator {
    ble/prompt/unit/add-hash '$_ble_edit_str'
    [[ $_ble_edit_str == *$'\n'* ]] || return 0
    ble/prompt/unit/add-hash '$bleopt_keymap_emacs_mode_string_multiline'
    local str=$bleopt_keymap_emacs_mode_string_multiline
    ble/prompt/unit/add-hash '${_ble_edit_arg:+1}${_ble_edit_kbdmacro_record:+1}'
    if [[ ! ${_ble_edit_arg:+1}${_ble_edit_kbdmacro_record:+1} ]]; then
      local keybinding_C_m=${_ble_decode_emacs_kmap_[_ble_decode_Ctrl|0x6d]}
      local keybinding_C_j=${_ble_decode_emacs_kmap_[_ble_decode_Ctrl|0x6a]}
      [[ $keybinding_C_m == *:ble/widget/accept-single-line-or-newline ]] &&
        [[ $keybinding_C_j == *:ble/widget/accept-line ]] &&
        str=${str:+"$str "}$'(\e[35mEnter\e[m 或 \e[35mCtrl+M\e[m：插入换行，\e[35mCtrl+J\e[m：执行)'
    fi
    [[ ! $str ]] || ble/prompt/print "$str"
  }
  ble-attach
else
  # 没有 ble.sh 时仍显式打开 Bash 的 bracketed paste，避免浏览器粘贴
  # 的 ESC[200~/ESC[201~ 标记被当成普通字符显示。
  bind 'set enable-bracketed-paste on' 2>/dev/null || true
fi

# Signal readiness after the interactive editor has attached. The descriptor is
# private to the Agent and never becomes part of terminal input or output.
if [[ ${NCP_TERMINAL_READY_FD:-} == 3 ]]; then
  printf 'ready\n' >&3 || true
  exec 3>&-
fi
