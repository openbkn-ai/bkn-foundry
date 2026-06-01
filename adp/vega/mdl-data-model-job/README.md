# Introduction 
TODO: Give a short introduction of your project. Let this section explain the objectives or the motivation behind this project. 

# Getting Started
TODO: Guide users through getting your code up and running on their own system. In this section you can talk about:
1.	Installation process
2.	Software dependencies
3.	Latest releases
4.	API references

# Build and Test

set env
```
export AUDIT_MODE_UT=true
export I18N_MODE_UT=true
export POD_IP='1.2.3.4'
export VERIFY_MODE_UT=true
```

lint check
```
golangci-lint run ./... 
```

run test
```
go test -gcflags=all=-l -v -coverprofile=ut_coverage.out ./...
go tool cover --html=report/ut_coverage.out -o ut_coverage.html

```

# Contribute
TODO: Explain how other users and developers can contribute to make your code better. 

If you want to learn more about creating good readme files then refer the following [guidelines](https://docs.microsoft.com/en-us/azure/devops/repos/git/create-a-readme?view=azure-devops). You can also seek inspiration from the below readme files:
- [ASP.NET Core](https://github.com/aspnet/Home)
- [Visual Studio Code](https://github.com/Microsoft/vscode)
- [Chakra Core](https://github.com/Microsoft/ChakraCore)
