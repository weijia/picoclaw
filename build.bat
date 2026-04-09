@echo off
setlocal

REM Set build variables
set BINARY_NAME=picoclaw
set BUILD_DIR=build
set CMD_DIR=cmd\%BINARY_NAME%

REM Set Go variables
set GO=go

REM Show build info
echo Building %BINARY_NAME%...
echo ========================

REM Create build directory
if not exist "%BUILD_DIR%" mkdir "%BUILD_DIR%"

REM Skip generate on Windows (cp command not available)
echo Skipping generate on Windows...
echo Generate skipped

REM Build executable
echo Building executable...
%GO% build -v -tags stdjson -o "%BUILD_DIR%\%BINARY_NAME%.exe" .\%CMD_DIR%
if %errorlevel% neq 0 (
    echo Build failed!
    pause
    exit /b %errorlevel%
)
echo Build completed: %BUILD_DIR%\%BINARY_NAME%.exe

echo ========================
echo Build successful!
echo Executable location: %BUILD_DIR%\%BINARY_NAME%.exe
echo ========================

REM Pause to view results
pause
