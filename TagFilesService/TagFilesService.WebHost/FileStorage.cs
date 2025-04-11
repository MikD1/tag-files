using Minio;
using Minio.DataModel.Args;

namespace TagFilesService.WebHost;

public class FileStorage(IMinioClient minioClient)
{
    public async Task<string> UploadFile(string bucketName, Stream fileStream, long fileSize, string contentType,
        string? fileName, string fileExtension)
    {
        // TODO: Validate fileName, fileExtension
        string objectName = (fileName ?? Guid.NewGuid().ToString().ToLower()) + fileExtension.ToLower();
        PutObjectArgs args = new PutObjectArgs()
            .WithBucket(bucketName)
            .WithObject(objectName)
            .WithStreamData(fileStream)
            .WithObjectSize(fileSize)
            .WithContentType(contentType);

        await minioClient.PutObjectAsync(args);
        return objectName;
    }
}