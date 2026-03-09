package backup

import (
	"bufio"
	"fmt"
	"os"
)


type Diff struct{
	Line int
	OldContent string
	NewContent string
}


func CompareFiles(file1, file2 string)(bool, []Diff, error) {

	f1 , err := os.Open(file1)

	if err != nil {
		return false , nil , fmt.Errorf("can not open file %s : %w" , f1.Name() , err)
	}
	defer f1.Close()

	f2 , err := os.Open(file2)

	if err != nil {
		return false , nil , fmt.Errorf("can not open file %s : %w" , f2.Name() , err)
	}
	defer f2.Close()

	scanner1 := bufio.NewScanner(f1)
	scanner2 := bufio.NewScanner(f2)


	lineNumber := 1
	var diffs []Diff
	identical := true

	for scanner1.Scan() && scanner2.Scan(){
		line1 := scanner1.Text()
		line2 := scanner2.Text()

		if line1 != line2 {
			identical = false
			diffs = append(diffs, Diff{
				Line: lineNumber,
				OldContent: line1,
				NewContent: line2,
			})
		}
		lineNumber++
	}
	//continue scan file1
    for scanner1.Scan() {
        identical = false
        diffs = append(diffs, Diff{
            Line:       lineNumber,
            OldContent: scanner1.Text(),
            NewContent: "",
        })
        lineNumber++
    }
	//continue scan file2
    for scanner2.Scan() {
        identical = false
        diffs = append(diffs, Diff{
            Line:       lineNumber,
            OldContent: "",
            NewContent: scanner2.Text(),
        })
        lineNumber++
    }


    if err := scanner1.Err(); err != nil {
        return false, nil, fmt.Errorf("error reading %s: %w", file1, err)
    }
    if err := scanner2.Err(); err != nil {
        return false, nil, fmt.Errorf("error reading %s: %w", file2, err)
    }

    return identical, diffs, nil

}