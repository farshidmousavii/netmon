package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/fatih/color"
)



type Logger struct{
	infoLog *log.Logger
	warnLog	*log.Logger
	errorLog *log.Logger
}

var globalLogger *Logger



func Init(logToFile bool) error {
    var err error
    globalLogger, err = newLogger(logToFile)
    return err
}

func GetLogger() *Logger {
    return globalLogger
}


func newLogger(logToFile bool)(*Logger , error){
	var writers []io.Writer

	//console
	writers = append(writers,os.Stdout)

	if logToFile {
		logFile , err := createLogFile()

		if err != nil {
			return nil , err
		}

		writers = append(writers, logFile)

	}

	multiWriter := io.MultiWriter(writers...)

	return &Logger{
		infoLog: log.New(multiWriter ,"[INFO]  ", log.LstdFlags),
        warnLog:  log.New(multiWriter, "[WARN]  ", log.LstdFlags),
        errorLog: log.New(multiWriter, "[ERROR] ", log.LstdFlags), 
	},nil

}


func createLogFile()(*os.File , error){

	if err := os.MkdirAll("log" , 0755) ; err != nil {
		return nil , err
	}
	filename := fmt.Sprintf("log/netmon_%s.log" , time.Now().Format("2006-01-02_15-04-05"))
	return os.OpenFile(filename , os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

}




func (l *Logger)Info(format string , v ...any){
	msg := fmt.Sprintf(format , v...)

	colorMsg := color.New(color.FgCyan).Sprintf("[INFO]  %s", msg)

	fmt.Println(colorMsg)


	l.infoLog.Println(msg)
}

func ( l *Logger)Warning(format string , v ...any){
	msg := fmt.Sprintf(format , v...)

	colorMsg := color.New(color.FgYellow).Sprintf("[WARN]  %s", msg)

	fmt.Println(colorMsg)


	l.warnLog.Println(msg)
}

func ( l *Logger)Error(format string , v ...any){
	msg := fmt.Sprintf(format , v...)

	colorMsg := color.New(color.FgRed).Sprintf("[ERROR]  %s", msg)

	fmt.Println(colorMsg)


	l.errorLog.Println(msg)
}


func Info(format string, v ...any) {
    if globalLogger != nil {
        globalLogger.Info(format, v...)
    }
}

func Warning(format string, v ...any) {
    if globalLogger != nil {
        globalLogger.Warning(format, v...)
    }
}

func Error(format string, v ...any) {
    if globalLogger != nil {
        globalLogger.Error(format, v...)
    }
}