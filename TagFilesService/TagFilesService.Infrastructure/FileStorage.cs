using Minio;
using Minio.DataModel;
using Minio.DataModel.Args;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class FileStorage(IMinioClient minioClient) : IFileStorage
{
    public async Task UploadFile(string bucketName, string fileName, Stream fileStream, long fileSize,
        string contentType)
    {
        PutObjectArgs args = new PutObjectArgs()
            .WithBucket(bucketName)
            .WithObject(fileName)
            .WithStreamData(fileStream)
            .WithObjectSize(fileSize)
            .WithContentType(contentType);

        await minioClient.PutObjectAsync(args);
    }

    public async Task<string> GeneratePresignedUrl(string bucketName, string fileName, TimeSpan expirationTime)
    {
        PresignedPutObjectArgs args = new PresignedPutObjectArgs()
            .WithBucket(bucketName)
            .WithObject(fileName)
            .WithExpiry((int)expirationTime.TotalSeconds);

        return await minioClient.PresignedPutObjectAsync(args);
    }

    public async Task<List<string>> ListFiles(string bucketName)
    {
        ListObjectsArgs args = new ListObjectsArgs()
            .WithBucket(bucketName)
            .WithPrefix(string.Empty);

        List<string> result = [];
        await foreach (Item item in minioClient.ListObjectsEnumAsync(args))
        {
            result.Add(item.Key);
        }

        return result;
    }

    public Task MoveFile(string sourceBucketName, string sourceFileName, string destinationBucketName)
    {
        throw new NotImplementedException();
    }
}