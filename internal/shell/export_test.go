package shell

// NewShell 只给测试用：不开窗口，用自定义 post 构造外壳。
var NewShell = newShell

// CloseShell 只给测试用：走和 Cmd+Q（OnBeforeQuit）同一条关闭路径。
var CloseShell = (*Shell).close
