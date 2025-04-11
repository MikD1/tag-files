using Minio;
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
}