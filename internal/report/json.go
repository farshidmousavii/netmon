package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/farshidmousavii/netmon/internal/logger"
)

func ReportToJson(allReports []DeviceReport) error {

	if err :=os.MkdirAll("reports" , 0755); err != nil {
		return fmt.Errorf("can not create report directory : %w" , err)
	}

	now := time.Now().Format("2006-01-02_15-04-05")
	fileName := fmt.Sprintf("report_%s.json" , now)
	filePath := filepath.Join("reports" , fileName)

	data, err := json.MarshalIndent(allReports, "", "  ")  
    if err != nil {
        return fmt.Errorf("failed to marshal reports: %w", err)
    }

    if err := os.WriteFile(filePath, data, 0644); err != nil {
        return fmt.Errorf("failed to write JSON file: %w", err)
    }

    logger.Info("JSON report saved to: %s", filePath)
    return nil
}