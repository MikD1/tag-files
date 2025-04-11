FROM mcr.microsoft.com/dotnet/sdk:9.0 AS build
WORKDIR /src

RUN apt-get -y update
RUN apt-get -y upgrade
RUN apt-get install -y ffmpeg

COPY TagFilesService/TagFilesService.sln ./
COPY TagFilesService/TagFilesService.Model/ TagFilesService.Model/
COPY TagFilesService/TagFilesService.Infrastructure/ TagFilesService.Infrastructure/
COPY TagFilesService/TagFilesService.Thumbnail/ TagFilesService.Thumbnail/
COPY TagFilesService/TagFilesService.WebHost/ TagFilesService.WebHost/

RUN dotnet restore "TagFilesService.WebHost/TagFilesService.WebHost.csproj"

RUN dotnet publish "TagFilesService.WebHost/TagFilesService.WebHost.csproj" -c Release -o /app/publish

# Этап рантайма
FROM mcr.microsoft.com/dotnet/aspnet:9.0 AS final
WORKDIR /app
COPY --from=build /app/publish .

EXPOSE 80
ENTRYPOINT ["dotnet", "TagFilesService.WebHost.dll"]
