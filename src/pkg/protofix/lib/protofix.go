package protofix

import(
        "fmt"
	"bytes"
	"strings"
	"go/printer"
        "go/parser"
        "go/token"
	"go/ast"
	"io/ioutil"
	"path/filepath"
	"os"
	"os/exec"
)

func FixAllPBGOFilesInDirectory(rootPath string) {

	filepath.Walk(rootPath, func (path string, f os.FileInfo, err error) error {
		if strings.HasSuffix(f.Name(), ".pb.go") {
			fmt.Printf("Repairing %v\n", path)
			repairFile(path)
		}
		return nil
	})

}

func RevertAllPBGOFilesInDirectory(rootPath string) {

	filepath.Walk(rootPath, func (path string, f os.FileInfo, err error) error {
		if f == nil {
			return nil
		}
		if strings.HasSuffix(f.Name(), ".pb.go") {
			fmt.Printf("Reverting %v\n", path)
			args := []string{"checkout", path}
			_, err := exec.Command("git", args... ).Output()
			if err != nil {
				fmt.Printf("Error reverting %v : %v\n", path, err)
				os.Exit(1)
			}
		}
		return nil
	})

}
        

func repairedFileBytes(filename string) []byte {
	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, filename, nil, parser.DeclarationErrors)

	if err != nil {
		fmt.Println(err)
		return nil
	}

	n := &lukeNodeWalker{}

	ast.Walk(n, f)

	var buf bytes.Buffer
	printer.Fprint(&buf, fset, f)

	return buf.Bytes()
}

func repairFile(filename string) {
	newFileContents := repairedFileBytes(filename)
	ioutil.WriteFile(filename, newFileContents, 0644)
}

func repairDeclaration(node ast.Node) {
	switch node := node.(type) {
	case *ast.Field:
		if len(node.Names) > 0 {
			declName := node.Names[0].Name
			if strings.HasSuffix(declName, "Id") {
				normalized := strings.TrimSuffix(declName, "Id")
				node.Names[0] = ast.NewIdent(fmt.Sprintf("%vID", normalized))
			}
		}

//	default:
//		fmt.Printf("the type is (%T)\n", node)
	}
	
}

type lukeNodeWalker struct {	
}

func (w *lukeNodeWalker) Visit(node ast.Node) (ast.Visitor) {
	repairDeclaration(node)
	return w
}