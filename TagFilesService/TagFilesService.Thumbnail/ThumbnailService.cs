using System.Diagnostics;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using TagFilesService.Model;

namespace TagFilesService.Thumbnail;

public class ThumbnailService(
    ILogger<ThumbnailService> logger,
    IServiceScopeFactory serviceScopeFactory) : IThumbnailService
{
    public void EnqueueThumbnailGeneration(uint fileId)
    {
        // TODO: Add job to queue, implement background processing
        Task.Run(async () => { await MakeThumbnail(fileId); });
    }

    private async Task MakeThumbnail(uint fileId)
    {
        using IServiceScope scope = serviceScopeFactory.CreateScope();
        IMetadataService metadataService = scope.ServiceProvider.GetRequiredService<IMetadataService>();
        IFileStorage fileStorage = scope.ServiceProvider.GetRequiredService<IFileStorage>();

        FileMetadata metadata = await metadataService.GetMetadata(fileId);

        // TODO: Get base url from configuration
        string fileUrl = "http://localhost:5010/library/" + metadata.FileName;

        using Process ffmpeg = new();
        ffmpeg.StartInfo = new()
        {
            FileName = "ffmpeg",
            Arguments = $"""-i "{fileUrl}" -vf scale=250:-1 -vframes 1 -f image2pipe -vcodec mjpeg -""",
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true
        };

        ffmpeg.Start();

        using MemoryStream thumbnailStream = new();
        await ffmpeg.StandardOutput.BaseStream.CopyToAsync(thumbnailStream);
        thumbnailStream.Seek(0, SeekOrigin.Begin);

        await ffmpeg.WaitForExitAsync();
        if (ffmpeg.ExitCode != 0)
        {
            string error = await ffmpeg.StandardError.ReadToEndAsync();
            throw new ApplicationException(error);
        }

        await fileStorage.UploadFile("thumbnail", metadata.FileName, thumbnailStream, thumbnailStream.Length,
            "image/jpeg");

        metadata.UpdateThumbnailStatus(ThumbnailStatus.Generated);
        await metadataService.SaveMetadata(metadata);
        logger.LogInformation("Thumbnail generated for file {FileId}", fileId);
    }
}