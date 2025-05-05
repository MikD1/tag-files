FROM mcr.microsoft.com/dotnet/sdk:9.0 AS build
WORKDIR /src

RUN apt-get -y update
RUN apt-get -y upgrade
RUN apt-get install -y ffmpeg

COPY TagFilesService.sln ./
COPY TagFilesService.Model/ TagFilesService.Model/
COPY TagFilesService.Infrastructure/ TagFilesService.Infrastructure/
COPY TagFilesService.Library/ TagFilesService.Library/
COPY TagFilesService.WebHost/ TagFilesService.WebHost/

RUN dotnet restore "TagFilesService.WebHost/TagFilesService.WebHost.csproj"

RUN dotnet publish "TagFilesService.WebHost/TagFilesService.WebHost.csproj" -c Release -o /app/publish

FROM mcr.microsoft.com/dotnet/aspnet:9.0 AS final
WORKDIR /app
COPY --from=build /app/publish .

EXPOSE 80
ENTRYPOINT ["dotnet", "TagFilesService.WebHost.dll"]
