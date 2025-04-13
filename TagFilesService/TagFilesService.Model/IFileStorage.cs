namespace TagFilesService.Model;

public interface IFileStorage
{
    Task UploadFile(string bucketName, string fileName, Stream fileStream, long fileSize, string contentType);

    Task<string> GeneratePresignedUrl(string bucketName, string fileName, TimeSpan expirationTime);

    Task<List<string>> ListFiles(string bucketName);

    Task MoveFile(string sourceBucketName, string sourceFileName, string destinationBucketName);
}