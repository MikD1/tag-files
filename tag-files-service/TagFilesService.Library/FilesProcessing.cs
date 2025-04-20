using System.Diagnostics;
using Microsoft.Extensions.Logging;
using Minio;
using Minio.DataModel.Args;
using Minio.DataModel.Tags;
using TagFilesService.Model;

namespace TagFilesService.Library;

public class FilesProcessing(ILogger<FilesProcessing> logger, IMinioClient minio, MetadataService metadataService)
{
    public async Task ProcessFile(string fileName)
    {
        string key = await AddFileToLibrary(fileName);
        await DeleteTemporaryFile(fileName);
        FileMetadata metadata = await SaveMetadata(key);
        await MakeThumbnail(metadata);
        logger.LogInformation("File '{fileName}' processed successfully", fileName);
    }

    private async Task<string> AddFileToLibrary(string fileName)
    {
        string key = MakeObjectKey(fileName);
        CopySourceObjectArgs sourceArgs = new CopySourceObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(fileName);
        CopyObjectArgs args = new CopyObjectArgs()
            .WithBucket(Buckets.Library)
            .WithObject(key)
            .WithCopyObjectSource(sourceArgs);
        await minio.CopyObjectAsync(args);
        logger.LogInformation("File '{key}' added to library", key);
        return key;
    }

    private async Task DeleteTemporaryFile(string fileName)
    {
        RemoveObjectArgs args = new RemoveObjectArgs()
            .WithBucket(Buckets.Temporary)
            .WithObject(fileName);
        await minio.RemoveObjectAsync(args);
        logger.LogInformation("Temporary file deleted");
    }

    private async Task<FileMetadata> SaveMetadata(string key)
    {
        // TODO: pass file type
        FileMetadata metadata = new(key, FileType.Unknown, null);
        await metadataService.SaveMetadata(metadata);

        Dictionary<string, string> tags = new() { ["metadata-id"] = metadata.Id.ToString() };
        SetObjectTagsArgs args = new SetObjectTagsArgs()
            .WithBucket(Buckets.Library)
            .WithObject(metadata.FileName)
            .WithTagging(Tagging.GetObjectTags(tags));
        await minio.SetObjectTagsAsync(args);

        logger.LogInformation("Metadata '{metadataId}' saved for file '{fileName}'", metadata.Id, metadata.FileName);
        return metadata;
    }

    private async Task MakeThumbnail(FileMetadata metadata)
    {
        try
        {
            string fileUrl = Path.Combine(minio.Config.Endpoint, Buckets.Library, metadata.FileName);

            using Process ffmpeg = new();
            ffmpeg.StartInfo = new()
            {
                FileName = "ffmpeg",
                Arguments = $"""-i "{fileUrl}" -vf scale=300:-1 -vframes 1 -f image2pipe -vcodec mjpeg -""",
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

            PutObjectArgs args = new PutObjectArgs()
                .WithBucket(Buckets.Thumbnail)
                .WithObject(metadata.FileName) // TODO: change extension to .jpg
                .WithStreamData(thumbnailStream)
                .WithObjectSize(thumbnailStream.Length)
                .WithContentType("image/jpeg");
            await minio.PutObjectAsync(args);

            metadata.UpdateThumbnailStatus(ThumbnailStatus.Generated);
            await metadataService.SaveMetadata(metadata);
            logger.LogInformation("Thumbnail generated for file {fileName}", metadata.FileName);
        }
        catch (Exception ex)
        {
            metadata.UpdateThumbnailStatus(ThumbnailStatus.Failed);
            await metadataService.SaveMetadata(metadata);
            logger.LogError("Failed to generate thumbnail for file {fileName}: {error}", metadata.FileName, ex.Message);
        }
    }

    private string MakeObjectKey(string originalKey)
    {
        string extension = Path.GetExtension(originalKey).ToLower();
        return Guid.NewGuid()
            .ToString()
            .Replace("-", string.Empty)
            .ToLower() + extension;
    }
}