using System.Diagnostics;
using MediatR;
using Microsoft.Extensions.Logging;
using Minio;
using TagFilesService.FilesProcessing.Contracts;
using TagFilesService.Infrastructure;
using TagFilesService.Model;
using TagFilesService.Model.Processing;

namespace TagFilesService.FilesProcessing.Handlers;

public class FileProcessingHandler(
    ILogger<FileProcessingHandler> logger,
    AppDbContext dbContext,
    IMinioClient minio,
    FilesProcessingQueue processingQueue)
    : IRequestHandler<FileProcessingRequest>
{
    public async Task Handle(FileProcessingRequest request, CancellationToken cancellationToken)
    {
        FileType fileType = GetFileType(request.ContentType);
        if (fileType is FileType.Unknown)
        {
            logger.LogInformation("Unknown file type. Try detecting with ffprobe");
            FileType? detectFileType = await TryDetectFileType(request.FileName, cancellationToken);
            if (detectFileType is not null)
            {
                logger.LogInformation("Detected file type: {FileType}", detectFileType);
                fileType = detectFileType.Value;
            }
        }

        ProcessingFile processingFile = new(request.FileName, fileType);
        await dbContext.AddAsync(processingFile, cancellationToken);
        await dbContext.SaveChangesAsync(cancellationToken);

        await processingQueue.Enqueue(processingFile, cancellationToken);
    }

    private async Task<FileType?> TryDetectFileType(string fileName, CancellationToken cancellationToken)
    {
        string fileUrl = Path.Combine(minio.Config.Endpoint, Buckets.Temporary, fileName);
        try
        {
            using Process ffprobeProcess = new();
            ffprobeProcess.StartInfo = new()
            {
                FileName = "ffprobe",
                Arguments =
                    $"-select_streams v -show_entries stream=codec_type -of default=noprint_wrappers=1:nokey=1 {fileUrl}",
                RedirectStandardOutput = true,
                RedirectStandardError = true,
                UseShellExecute = false,
                CreateNoWindow = true
            };

            ffprobeProcess.Start();
            await ffprobeProcess.WaitForExitAsync(cancellationToken);
            if (ffprobeProcess.ExitCode == 0)
            {
                string result = await ffprobeProcess.StandardOutput.ReadToEndAsync(cancellationToken);
                result = result.Trim();
                logger.LogInformation("ffprobe result: {Result}", result);

                if (result == "video")
                {
                    return FileType.Video;
                }
            }
            else
            {
                string error = await ffprobeProcess.StandardError.ReadToEndAsync(cancellationToken);
                logger.LogError("ffprobe error: {Error}", error);
            }
        }
        catch (Exception ex)
        {
            logger.LogError(ex, "Error detecting file type");
        }

        return null;
    }

    private FileType GetFileType(string contentType)
    {
        if (contentType.StartsWith("image/"))
        {
            return FileType.Image;
        }

        if (contentType.StartsWith("video/"))
        {
            return FileType.Video;
        }

        return FileType.Unknown;
    }
}