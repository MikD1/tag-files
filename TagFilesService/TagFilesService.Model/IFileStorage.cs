namespace TagFilesService.Model;

public interface IFileStorage
{
    Task<string> GeneratePresignedUrl(string bucketName, string fileName, TimeSpan expirationTime);
}