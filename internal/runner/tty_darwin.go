package runner

// TIOCGETA. The BSDs read termios through a different ioctl number than Linux.
const ioctlReadTermios = 0x40487413
