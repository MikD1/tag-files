using Minio;
using Minio.DataModel.Args;
using TagFilesService.Model;

namespace TagFilesService.Infrastructure;

public class FileStorage(IMinioClient minioClient) : IFileStorage
{
    public async Task<string> GeneratePresignedUrl(string bucketName, string fileName, TimeSpan expirationTime)
    {
        PresignedPutObjectArgs args = new PresignedPutObjectArgs()
            .WithBucket(bucketName)
            .WithObject(fileName)
            .WithExpiry((int)expirationTime.TotalSeconds);

        return await minioClient.PresignedPutObjectAsync(args);
    }
}